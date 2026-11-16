package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
	"unicode/utf16"
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/conversation"
	"inbok/internal/search"
	"inbok/internal/session"
)

// searchService searches members' mail, e.g. [search.Searcher].
type searchService interface {
	Search(ctx context.Context, owner int64, request search.Request) (search.Page, error)
}

type searchHandler struct {
	searches searchService
	running  *runningSearches
	logger   *slog.Logger
}

func registerSearch(api huma.API, logger *slog.Logger, searches searchService) {
	registerConversationSearch(api, searchHandler{searches: searches, running: newRunningSearches(maxRunningSearches), logger: logger})
}

// ========================
// Conversation search
// ========================

const (
	searchUnavailableType = problemPrefix + "search-unavailable"
	searchesRunningType   = problemPrefix + "searches-running"
)

// searchUnavailableRetry is the Retry-After of a search the index did not
// answer, inbok's choice: about how long Meilisearch takes to start again.
const searchUnavailableRetry = 15 * time.Second

// maxRunningSearches is how many searches one member may have running at
// once, inbok's choice: the web runs one per search box, but a search the
// member typed past still runs while the next starts, until it ends. A member past it, e.g. a
// script, takes no more of the index's time from other members.
const maxRunningSearches = 2

// runningSearchRetry is the Retry-After of a search refused for
// [maxRunningSearches]: a search usually ends within a second.
const runningSearchRetry = time.Second

// searchRunTimeout bounds a search that runs on after its request ended,
// so a search always ends and gives its place back. It outlasts
// [requestTimeout], so a slow search still answers that the server is busy.
const searchRunTimeout = requestTimeout + 5*time.Second

// hasAttachment is the has parameter's one value.
const hasAttachment = "attachment"

type conversationSearchInput struct {
	// 200 characters is inbok's choice: only a query's first 10 words
	// count, and no 10 words of mail need more.
	Q       string `query:"q" pattern:"\\S" maxLength:"200" doc:"The words to find, as the member typed them. A word a typo or two away matches, but a number only exactly; the last word also matches as the start of a longer word; \"quoted words\" match as a phrase; -word leaves out what has it. Every word must match; only the first 10 count. Left out, every message the other parameters keep matches, newest first."`
	Account int64  `query:"account" minimum:"1" doc:"A connected account's id, to search only its mail; every account's when left out."`
	// 20 places is inbok's choice: every view and more labels than a
	// member ticks, while each place adds a clause the index checks for
	// every message.
	Place []string `query:"place,explode" nullable:"false" maxItems:"20" doc:"Where to search: a view, inbox, starred, sent, all, spam, or trash, or the id of one of the account's labels from List Places, each as List Conversations reads it. A conversation in any of them matches. Repeat it for each, up to 20. Left out, all: every conversation but those only in Spam or Trash."`
	// places are Place parsed, by Resolve.
	places []conversation.Place
	// 10 senders is inbok's choice: a person's or a company's few
	// addresses. 254 characters is inbok's choice too, after the 254
	// octets RFC 5321 leaves a mailbox in an SMTP path.
	From []string `query:"from,explode" nullable:"false" maxItems:"10" format:"idn-email" maxLength:"254" doc:"Senders, by email address, as a message's from lists them: a message from any of them matches, ignoring case. Repeat it for each, up to 10."`
	// Instants with their offset, not dates: a day starts at another
	// moment in each time zone, which the client knows.
	Since  time.Time `query:"since" doc:"Only messages the provider received at or after this instant, e.g. the member's local midnight, as 2026-11-01T00:00:00+07:00."`
	Before time.Time `query:"before" doc:"Only messages the provider received before this instant, which must be later than since."`
	Has    string    `query:"has" enum:"attachment" doc:"attachment: only messages with a file to download, e.g. a PDF or a photo, but not a logo shown in the text. Only messages whose content inbok has fetched can match: most mail of the last 90 days outside Spam and Trash, a while after it arrives, and any message opened."`
	// A page past 1000 holds nothing: a search counts at most
	// [search.MaxTotal] matches, which TestSearchConversationsBounds ties to it.
	Page int `query:"page" minimum:"1" maximum:"1000" default:"1" doc:"Which page of results, from 1."`
	// 50 is inbok's choice: the web's results page, and an index's time
	// per search grows with each result's body text it cuts.
	Limit int `query:"limit" minimum:"1" maximum:"50" default:"50" doc:"How many results a page holds, at most."`
}

// Resolve parses the places, and refuses what Huma lets through: a list
// of senders whose first is empty, and an instant given that equals Go's
// zero time, which every layer below reads as no bound. Huma calls it
// after parsing the request, before the handler.
//
// DESIGN: Huma decides whether a parameter is there from its first value
// alone, so a list that starts with an empty sender skips the whole
// parameter, its checks included, and would search every sender. Any
// other list Huma checks itself, a value that is no address anywhere
// included, so only that one case is refused here; the rest of such a
// list is checked once the first value is fixed. Places are read from the
// URL for the same reason: an empty first place would search All mail.
func (in *conversationSearchInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	query := u.Query()
	var errs []error
	in.places = nil
	for n, text := range query["place"] {
		place, err := conversation.ParsePlace(text)
		if err != nil {
			errs = append(errs, &huma.ErrorDetail{Location: "query.place[" + strconv.Itoa(n) + "]", Message: msgExpectedPlace})
			continue
		}
		in.places = append(in.places, place)
	}
	if senders := query["from"]; len(senders) > 0 && senders[0] == "" {
		errs = append(errs, &huma.ErrorDetail{Location: "query.from[0]", Message: "expected an email address"})
	}
	bounds := []struct {
		name string
		at   time.Time
	}{{"since", in.Since}, {"before", in.Before}}
	for _, bound := range bounds {
		if query.Get(bound.name) != "" && bound.at.IsZero() {
			errs = append(errs, &huma.ErrorDetail{Location: "query." + bound.name, Message: "expected an instant other than Go's zero time"})
		}
	}
	return errs
}

// textMatch is where a search matched a text.
type textMatch struct {
	Start  int `json:"start" minimum:"0" doc:"Where the match starts, in UTF-16 code units from the text's start, as a JavaScript string counts."`
	Length int `json:"length" minimum:"1" doc:"How long the match is, in UTF-16 code units."`
}

// searchMatch is the message a search result matched through, as the
// result shows it.
type searchMatch struct {
	MessageID      uuid.UUID     `json:"messageId" format:"uuid" doc:"The message's id."`
	Date           time.Time     `json:"date" doc:"When the provider received it."`
	From           []mailAddress `json:"from" nullable:"false" doc:"Its From, perhaps empty."`
	Subject        string        `json:"subject" doc:"Its subject, as sent."`
	SubjectMatches []textMatch   `json:"subjectMatches" nullable:"false" doc:"Where the search matched the subject, in order, none overlapping."`
	Preview        string        `json:"preview" doc:"Its body's text around the best match, starting or ending with an ellipsis where cut, when the search matched its body; otherwise a plain-text excerpt of its body, as the provider made it."`
	PreviewMatches []textMatch   `json:"previewMatches" nullable:"false" doc:"Where the search matched the preview, in order, none overlapping; empty when it shows the provider's excerpt."`
}

// foundConversation is one search result: a conversation as a list shows
// it, and its best-matching message.
type foundConversation struct {
	Conversation conversationRow `json:"conversation" doc:"The conversation, as List Conversations shows it."`
	Match        searchMatch     `json:"match" doc:"Its best-matching message, or without q its newest, which passes every filter itself."`
}

type conversationSearchPage struct {
	Results []foundConversation `json:"results" nullable:"false" doc:"Most relevant first, each conversation once. A page can hold fewer than limit and still not be the last: keep paging while page times limit is below total."`
	Total   int                 `json:"total" minimum:"0" maximum:"1000" doc:"How many conversations match, over every page; 1000 means 1000 or more."`
}

func registerConversationSearch(api huma.API, handler searchHandler) {
	responses := placeResponses(api, http.StatusTooManyRequests, http.StatusServiceUnavailable)
	tooMany := responses[strconv.Itoa(http.StatusTooManyRequests)]
	tooMany.Description = "The signed-in account already has as many searches running as it may; Retry-After says when to try again."
	tooMany.Content[problemContentType].Example = searchesRunningProblem()
	unavailable := responses[strconv.Itoa(http.StatusServiceUnavailable)]
	unavailable.Description = "The search index is not answering, e.g. while it restarts."
	unavailable.Content[problemContentType].Example = searchUnavailableProblem()
	huma.Register(api, huma.Operation{
		OperationID: "searchConversations",
		Method:      http.MethodGet,
		Path:        conversationsPath + "/search",
		Summary:     "Search Conversations",
		Tags:        []string{tagConversations},
		Description: "Finds the signed-in account's conversations in any of the places given, across its connected accounts or one of them, " +
			"with a message whose subject, sender, recipients, or body text match q, most relevant first, and that passes every filter given: from, since, before, and has. " +
			"Without q, every message the filters keep matches, newest first. " +
			"Each conversation appears once, through its best-matching message, or without q its newest. " +
			"A place holds the conversations List Conversations lists there, found through their messages in neither Spam, Trash, nor drafts; " +
			"spam and trash through any message but a draft. A message whose body is not stored yet is found by its headers. " +
			"The search index catches up with new mail within moments, so a message synchronized a moment ago may not be found yet. " +
			"The signed-in account may have two searches running at once.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleSearchConversations)
}

// searchOutcome is what a search running apart from its request returns.
type searchOutcome struct {
	page search.Page
	err  error
}

func (h searchHandler) handleSearchConversations(ctx context.Context, in *conversationSearchInput) (*output[conversationSearchPage], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User

	// 1. Refuse a range nothing is in before taking a place, so the answer
	// says what to correct rather than to wait.
	if !in.Since.IsZero() && !in.Before.IsZero() && !in.Since.Before(in.Before) {
		return nil, badRangeProblem()
	}

	// 2. Take one of the member's places, or refuse before asking the
	// index anything.
	if !h.running.take(user.ID) {
		return nil, huma.ErrorWithHeaders(searchesRunningProblem(), http.Header{retryAfterHeader: {retryAfterSeconds(runningSearchRetry)}})
	}

	// 3. Search apart from the request, giving the place back only when the
	// search ends: a client that gives up would otherwise free its place
	// while the index still works on its search, and could send the next.
	done := make(chan searchOutcome, 1)
	request := search.Request{
		Text:           in.Q,
		Account:        in.Account,
		Places:         in.places,
		From:           in.From,
		Since:          in.Since,
		Before:         in.Before,
		WithAttachment: in.Has == hasAttachment,
		Page:           in.Page,
		Limit:          in.Limit,
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), searchRunTimeout)
		defer cancel()
		var outcome searchOutcome
		defer func() {
			// withRecovery covers only the request's own goroutine: a panic
			// here would end the process. Logged here, as the request may
			// have ended.
			if v := recover(); v != nil {
				h.logger.ErrorContext(runCtx, "search panicked", slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
				outcome = searchOutcome{err: errSearchPanicked}
			}
			// Free the place before answering, so the client's next search
			// finds it free.
			h.running.release(user.ID)
			done <- outcome
		}()
		outcome.page, outcome.err = h.searches.Search(runCtx, user.ID, request)
	}()

	var outcome searchOutcome
	select {
	case outcome = <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if outcome.err != nil {
		return nil, searchProblem(outcome.err)
	}
	out := conversationSearchPage{Results: make([]foundConversation, 0, len(outcome.page.Found)), Total: outcome.page.Total}
	for _, f := range outcome.page.Found {
		out.Results = append(out.Results, foundConversation{
			Conversation: conversationRowOf(f.Conversation),
			Match: searchMatch{
				MessageID:      f.Message.ID,
				Date:           f.Message.Date,
				From:           mailAddressesOf(f.Message.From),
				Subject:        f.Message.Subject,
				SubjectMatches: textMatchesOf(f.Message.Subject, f.SubjectMatches),
				Preview:        f.Preview,
				PreviewMatches: textMatchesOf(f.Preview, f.PreviewMatches),
			},
		})
	}
	return respondData(out), nil
}

// textMatchesOf returns spans, byte offsets in text, as UTF-16 offsets.
// Each span lies in text and starts and ends on a character's first byte,
// as [search.Found] promises.
func textMatchesOf(text string, spans []search.Span) []textMatch {
	out := make([]textMatch, 0, len(spans))
	for _, s := range spans {
		start := utf16Length(text[:s.Start])
		out = append(out, textMatch{Start: start, Length: utf16Length(text[s.Start : s.Start+s.Length])})
	}
	return out
}

// utf16Length returns how many UTF-16 code units text takes, as
// JavaScript counts a string: two for a character past U+FFFF, e.g. most
// emoji, and one for any other.
func utf16Length(text string) int {
	n := 0
	// Ranging over a string reads a byte that is not UTF-8 as U+FFFD, as
	// encoding/json writes it, so RuneLen never returns -1 here.
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}

// searchProblem maps the errors of a search.
func searchProblem(err error) error {
	switch {
	case errors.Is(err, search.ErrAccountNotFound):
		return placeProblem(conversation.ErrAccountNotFound)
	case errors.Is(err, conversation.ErrPlaceNotFound):
		return placeProblem(err)
	case errors.Is(err, search.ErrBadRange):
		return badRangeProblem()
	case errors.Is(err, search.ErrUnavailable):
		return huma.ErrorWithHeaders(searchUnavailableProblem(), http.Header{retryAfterHeader: {retryAfterSeconds(searchUnavailableRetry)}})
	default:
		return err
	}
}

// badRangeProblem returns the 422 for a since not before before, on
// before, the bound a member changes; its text names no parameter.
func badRangeProblem() *problem {
	invalid := requestProblem(http.StatusUnprocessableEntity)
	invalid.Errors = []inputError{{Location: "query.before", Message: "Choose an end after the start."}}
	return invalid
}

// errSearchPanicked is a search that panicked, logged where it did.
var errSearchPanicked = errors.New("search panicked")

// searchesRunningProblem returns the 429 for a search past
// [maxRunningSearches]; the handler adds its Retry-After.
func searchesRunningProblem() *problem {
	return newProblem(http.StatusTooManyRequests, searchesRunningType, "Too many searches", "Too many searches are running for this account. Try again in a moment.")
}

// searchUnavailableProblem returns the 503 for a search the index did not
// answer; [searchProblem] adds its Retry-After.
func searchUnavailableProblem() *problem {
	return newProblem(http.StatusServiceUnavailable, searchUnavailableType, "Search unavailable", "Search is not available right now. Try again shortly.")
}

// runningSearches counts each member's running searches, in this process.
// It is safe for concurrent use.
type runningSearches struct {
	mu      sync.Mutex
	limit   int
	members map[int64]int // members with a search running, never 0
}

// newRunningSearches returns counts that let each member run at most
// limit searches at once.
func newRunningSearches(limit int) *runningSearches {
	return &runningSearches{limit: limit, members: map[int64]int{}}
}

// take counts one more running search of member's and returns true, or
// returns false when member already runs the limit.
func (r *runningSearches) take(member int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.members[member] >= r.limit {
		return false
	}
	r.members[member]++
	return true
}

// release counts one of member's searches as ended.
func (r *runningSearches) release(member int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.members[member]--; r.members[member] <= 0 {
		delete(r.members, member)
	}
}
