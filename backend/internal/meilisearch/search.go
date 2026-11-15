package meilisearch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"uuid"

	meili "github.com/meilisearch/meilisearch-go"

	"inbok/internal/search"
)

// excerptWords is how many words of body text a hit's excerpt holds,
// inbok's choice: about two lines of a wide list.
const excerptWords = 20

// The marks Meilisearch puts around each match, which Go takes out again:
// STX and ETX, two control characters body text never holds, as
// [search.BodyText] drops them. A subject that holds one anyway loses its
// matches, as a caller compares the unmarked subject with storage's.
const (
	markStart = "\x02"
	markEnd   = "\x03"
)

// hit is what a search returns of each document.
type hit struct {
	ID        string    `json:"id"`
	Formatted formatted `json:"_formatted"`
}

// formatted is a hit's subject, and its body texts each cut to around its
// best match, with every match marked.
type formatted struct {
	Subject  string `json:"subject"`
	BodyText string `json:"body_text"`
	AltText  string `json:"alt_text"`
}

// Search returns one page of owner's messages that match every word of
// query and its filters, most relevant first, or with no words newest
// first, one per conversation, and no matches for a member with no index
// yet. It returns [search.ErrNoOwner], [search.ErrBadPage], or
// [search.ErrBadRange] before searching.
func (i *Index) Search(ctx context.Context, owner int64, query search.Query) (search.Result, error) {
	if err := search.ValidateQuery(owner, query); err != nil {
		return search.Result{}, err
	}
	page, size := pageWithin(query)
	request := &meili.SearchRequest{
		Filter: filter(owner, query),
		// page and hitsPerPage, unlike offset and limit, count every match
		// instead of estimating.
		Page:        page,
		HitsPerPage: &size,
		// NOTE: with distinct, both count conversations, not messages, which
		// Meilisearch's docs do not say: v1.56.1's source does it, and the
		// contract's "one per conversation" test checks it.
		Distinct: fieldConversation,
		// Each word narrows a search, as in Gmail: by default Meilisearch
		// drops the last words until something matches.
		MatchingStrategy:     meili.All,
		AttributesToRetrieve: []string{fieldID},
	}
	// With no words there is nothing to mark, and cutting excerpts is most
	// of a search's time, so a search without words asks for IDs alone.
	if strings.TrimSpace(query.Text) != "" {
		// Marks rather than showMatchesPosition, which lists every match in
		// both body texts, up to 64 KiB each: a crafted mailbox made one
		// page of 100 decode to about 500 MB.
		request.AttributesToCrop = []string{fieldBodyText, fieldAltText}
		request.CropLength = excerptWords
		request.CropMarker = search.ExcerptCut
		request.AttributesToHighlight = []string{fieldSubject, fieldBodyText, fieldAltText}
		request.HighlightPreTag = markStart
		request.HighlightPostTag = markEnd
	}
	response, err := i.client.Index(i.uid(owner)).SearchWithContext(ctx, query.Text, request)
	if err != nil {
		err = describe(ctx, "search", err)
		if errors.Is(err, errIndexMissing) {
			return search.Result{Hits: []search.Hit{}}, nil
		}
		return search.Result{}, err
	}

	var hits []hit
	if err := response.Hits.DecodeInto(&hits); err != nil {
		return search.Result{}, fmt.Errorf("read search hits: %w", err)
	}
	found := make([]search.Hit, len(hits))
	for n, h := range hits {
		id, err := uuid.Parse(h.ID)
		if err != nil {
			return search.Result{}, fmt.Errorf("read search hit %d: %w", n, err)
		}
		subject, matches := unmark(h.Formatted.Subject)
		found[n] = search.Hit{ID: id, Subject: subject, SubjectMatches: complete(subject, matches)}
		// The text a view shows first; the rest when only it matched.
		for _, text := range []string{h.Formatted.BodyText, h.Formatted.AltText} {
			if excerpt, matches := unmark(text); len(matches) > 0 {
				found[n].Excerpt, found[n].ExcerptMatches = excerpt, complete(excerpt, matches)
				break
			}
		}
	}
	// NOTE: totalHits counts past pagination.maxTotalHits, which only caps
	// the hits a search can page through.
	total := min(response.TotalHits, search.MaxTotal)
	return search.Result{Hits: found, Total: int(total)}, nil
}

// maxTypos is the most typos Meilisearch allows in a word, in its typo
// tolerance docs.
const maxTypos = 2

// complete returns matches in text, each reaching the end of its word when
// at most [maxTypos] letters or digits of the word are left past it.
//
// DESIGN: Meilisearch matches a query's last word as a prefix too, and
// marks as many characters of it as the query word has, so a typo there
// marks part of a word, e.g. "anggran" marks "Anggara" of "Anggaran", and
// so does a prefix, e.g. "inv" marks "Inv" of "Invoice", which it means to
// (meilisearch/meilisearch#1441). It marks the other words whole.
// Engines mark a prefix as typed and any other match as the whole word. A
// typo leaves at most
// maxTypos characters of its word unmarked, so completing only that much
// marks every typo whole and leaves a prefix as typed, but for one within
// maxTypos of its word's end. It never reads the query, and in a script
// written without spaces, where a run of letters can be a whole
// sentence, it adds at most maxTypos characters.
func complete(text string, matches []search.Span) []search.Span {
	out := make([]search.Span, len(matches))
	for n, m := range matches {
		out[n] = m
		end := m.Start + m.Length
		if end < 0 || end > len(text) {
			continue
		}
		at := end
		for range maxTypos {
			r, size := utf8.DecodeRuneInString(text[at:])
			if size == 0 || !inWord(r) {
				break
			}
			at += size
		}
		if r, _ := utf8.DecodeRuneInString(text[at:]); at == len(text) || !inWord(r) {
			out[n].Length = at - m.Start
		}
	}
	return out
}

// inWord reports whether r can be part of a word: a letter, a digit, or a
// mark, e.g. a combining accent.
func inWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// unmark returns text without [markStart] and [markEnd], and where the
// text they marked lies in what is left. A start with no end marks to the
// end of the text.
func unmark(text string) (string, []search.Span) {
	var out strings.Builder
	var matches []search.Span
	open := -1
	for len(text) > 0 {
		switch {
		case strings.HasPrefix(text, markStart):
			text = text[len(markStart):]
			if open < 0 {
				open = out.Len()
			}
			continue
		case strings.HasPrefix(text, markEnd):
			text = text[len(markEnd):]
			if open >= 0 && out.Len() > open {
				matches = append(matches, search.Span{Start: open, Length: out.Len() - open})
			}
			open = -1
			continue
		}
		out.WriteByte(text[0])
		text = text[1:]
	}
	if open >= 0 && out.Len() > open {
		matches = append(matches, search.Span{Start: open, Length: out.Len() - open})
	}
	return out.String(), matches
}

// pageWithin returns query's page and page size as Meilisearch takes them,
// with the same hits: a page holds at most [search.MaxTotal], and a page
// past the last one with hits becomes the first such page.
//
// NOTE: Meilisearch computes (page - 1) * hitsPerPage without checking for
// overflow, so a far page, e.g. 2^62 + 1 of 4, wraps around to the first
// page on v1.56.1.
func pageWithin(query search.Query) (page, size int64) {
	size = int64(min(query.PageSize, search.MaxTotal))
	// The first page whose offset is at least MaxTotal, so it has no hits.
	beyond := (search.MaxTotal-1)/size + 2
	return min(int64(query.Page), beyond), size
}

// filter returns a search's filter, a list whose every expression must
// hold: always the owner first, then the accounts, mailboxes, senders,
// dates, and attachment query names. Each value from the member is quoted
// whole inside its own expression, never joined to another with OR
// outside a list.
func filter(owner int64, query search.Query) []string {
	// The owner too, although the index is already the member's own: a
	// second guard, should a document ever reach the wrong index.
	expressions := []string{fieldOwner + " = " + quote(decimal(owner))}
	if len(query.Accounts) > 0 {
		ids := make([]string, len(query.Accounts))
		for n, id := range query.Accounts {
			ids[n] = quote(decimal(id))
		}
		expressions = append(expressions, fieldAccount+" IN ["+strings.Join(ids, ", ")+"]")
	}
	if len(query.ExcludeMailboxes) > 0 {
		ids := make([]string, len(query.ExcludeMailboxes))
		for n, id := range query.ExcludeMailboxes {
			ids[n] = quote(id.String())
		}
		// On a list field, IN holds when any of its values is listed.
		expressions = append(expressions, fieldMailboxes+" NOT IN ["+strings.Join(ids, ", ")+"]")
	}
	if len(query.From) > 0 {
		addresses := make([]string, len(query.From))
		for n, address := range query.From {
			addresses[n] = quote(address)
		}
		// Meilisearch's = and IN ignore case on strings, as its filter
		// reference says.
		expressions = append(expressions, fieldFromAddresses+" IN ["+strings.Join(addresses, ", ")+"]")
	}
	if !query.Since.IsZero() {
		expressions = append(expressions, fieldDate+" >= "+strconv.FormatInt(milliUp(query.Since), 10))
	}
	if !query.Before.IsZero() {
		expressions = append(expressions, fieldDate+" < "+strconv.FormatInt(milliUp(query.Before), 10))
	}
	if query.WithAttachment {
		expressions = append(expressions, fieldAttachment+" = true")
	}
	return expressions
}

// milliUp returns t in Unix milliseconds, the index's dates, rounded up.
//
// DESIGN: a date in the index is a whole number of milliseconds, so it is
// at or after t, or before t, exactly when it is so against t rounded up:
// one rounding serves both bounds.
func milliUp(t time.Time) int64 {
	ms := t.UnixMilli()
	if t.Nanosecond()%int(time.Millisecond) != 0 {
		ms++
	}
	return ms
}

// quote returns value as a double-quoted string in a filter.
//
// NOTE: Meilisearch's filter docs do not say how to escape. On v1.56.1 a
// backslash escapes a double quote or a backslash.
func quote(value string) string {
	// 1. Double each backslash, before step 2 adds backslashes of its own.
	value = strings.ReplaceAll(value, `\`, `\\`)
	// 2. Escape each double quote.
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
