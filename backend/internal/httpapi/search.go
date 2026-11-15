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
	"uuid"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/connectedaccount"
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
	registerMessageSearch(api, searchHandler{searches: searches, running: newRunningSearches(maxRunningSearches), logger: logger})
}

// ========================
// Message search
// ========================

const (
	searchUnavailableType = problemPrefix + "search-unavailable"
	searchesRunningType   = problemPrefix + "searches-running"
)

// searchUnavailableRetry is the Retry-After of a search the index did not
// answer, inbok's choice: about how long Meilisearch takes to start again.
const searchUnavailableRetry = 15 * time.Second

// maxRunningSearches is how many searches one member may have running at
// once, inbok's choice: the web runs at most one per search box, and the
// list's box and the results page make two. A member past it, e.g. a
// script, takes no more of the index's time from other members.
const maxRunningSearches = 2

// runningSearchRetry is the Retry-After of a search refused for
// [maxRunningSearches]: a search usually ends within a second.
const runningSearchRetry = time.Second

// searchRunTimeout bounds a search that runs on after its request ended,
// so a search always ends and gives its place back. It outlasts
// [requestTimeout], so a slow search still answers that the server is busy.
const searchRunTimeout = requestTimeout + 5*time.Second

type messageSearchInput struct {
	// 200 characters is inbok's choice: only a query's first 10 words
	// count, and no 10 words of mail need more.
	Q       string `query:"q" required:"true" pattern:"\\S" maxLength:"200" doc:"The words to find, as the member typed them. A word a typo or two away matches, but a number only exactly; the last word also matches as the start of a longer word; \"quoted words\" match as a phrase; -word leaves out what has it. Every word must match; only the first 10 count."`
	Account int64  `query:"account" minimum:"1" doc:"A connected account's id, to search only its mail; every account's when left out."`
	// A page past 1000 holds nothing: a search counts at most
	// [search.MaxTotal] matches, which TestSearchMessagesBounds ties to it.
	Page int `query:"page" minimum:"1" maximum:"1000" default:"1" doc:"Which page of results, from 1."`
	// 50 is inbok's choice: the web's results page, and an index's time
	// per search grows with each result's body text it cuts.
	Limit int `query:"limit" minimum:"1" maximum:"50" default:"50" doc:"How many results a page holds, at most."`
}

// textMatch is where a search matched a text.
type textMatch struct {
	Start  int `json:"start" minimum:"0" doc:"Where the match starts, in UTF-16 code units from the text's start, as a JavaScript string counts."`
	Length int `json:"length" minimum:"1" doc:"How long the match is, in UTF-16 code units."`
}

// foundMessage is one search result: a conversation's best-matching
// message, as a list row shows it.
type foundMessage struct {
	ID             uuid.UUID     `json:"id" format:"uuid" doc:"The message's id."`
	AccountID      int64         `json:"accountId" doc:"The connected account it belongs to."`
	Date           time.Time     `json:"date" doc:"When the provider received it."`
	From           []mailAddress `json:"from" nullable:"false" doc:"Its From, perhaps empty."`
	Subject        string        `json:"subject" doc:"Its subject, as sent."`
	SubjectMatches []textMatch   `json:"subjectMatches" nullable:"false" doc:"Where the search matched the subject, in order, none overlapping."`
	Preview        string        `json:"preview" doc:"Its body's text around the best match, starting or ending with an ellipsis where cut, when the search matched its body; otherwise a plain-text excerpt of its body, as the provider made it."`
	PreviewMatches []textMatch   `json:"previewMatches" nullable:"false" doc:"Where the search matched the preview, in order, none overlapping; empty when it shows the provider's excerpt."`
	Unread         bool          `json:"unread" doc:"Whether it is unread."`
	Starred        bool          `json:"starred" doc:"Whether it is starred."`
}

type messageSearchPage struct {
	Messages []foundMessage `json:"messages" nullable:"false" doc:"Most relevant first, at most one per conversation. A page can hold fewer than limit and still not be the last: keep paging while page times limit is below total."`
	Total    int            `json:"total" minimum:"0" maximum:"1000" doc:"How many conversations match, over every page; 1000 means 1000 or more."`
}

func registerMessageSearch(api huma.API, handler searchHandler) {
	responses := memberResponses(api, http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable)
	accountNotFoundResponse(responses)
	tooMany := responses[strconv.Itoa(http.StatusTooManyRequests)]
	tooMany.Description = "The signed-in account already has as many searches running as it may; Retry-After says when to try again."
	tooMany.Content[problemContentType].Example = searchesRunningProblem()
	unavailable := responses[strconv.Itoa(http.StatusServiceUnavailable)]
	unavailable.Description = "The search index is not answering, e.g. while it restarts."
	unavailable.Content[problemContentType].Example = searchUnavailableProblem()
	huma.Register(api, huma.Operation{
		OperationID: "searchMessages",
		Method:      http.MethodGet,
		Path:        messagesPath + "/search",
		Summary:     "Search Messages",
		Tags:        []string{tagMessages},
		Description: "Finds the signed-in account's messages whose subject, sender, recipients, or body text match q, most relevant first, " +
			"across its connected accounts or one of them. Each conversation appears once, through its best-matching message. " +
			"Messages in Spam or Trash, and drafts, are left out. A message whose body is not stored yet is found by its headers. " +
			"The search index catches up with new mail within moments, so a message synchronized a moment ago may not be found yet. " +
			"The signed-in account may have two searches running at once.",
		Metadata:  map[string]any{accessKey: session.AccessMember},
		Security:  []map[string][]string{{sessionSecuritySchemeName: {}}},
		Responses: responses,
	}, handler.handleSearchMessages)
}

// searchOutcome is what a search running apart from its request returns.
type searchOutcome struct {
	page search.Page
	err  error
}

func (h searchHandler) handleSearchMessages(ctx context.Context, in *messageSearchInput) (*output[messageSearchPage], error) {
	user := ctx.Value(sessionKey{}).(session.Authenticated).Identity.User

	// 1. Take one of the member's places, or refuse before asking the
	// index anything.
	if !h.running.take(user.ID) {
		return nil, huma.ErrorWithHeaders(searchesRunningProblem(), http.Header{retryAfterHeader: {retryAfterSeconds(runningSearchRetry)}})
	}

	// 2. Search apart from the request, giving the place back only when the
	// search ends: a client that gives up would otherwise free its place
	// while the index still works on its search, and could send the next.
	done := make(chan searchOutcome, 1)
	request := search.Request{Text: in.Q, Account: in.Account, Page: in.Page, Limit: in.Limit}
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
	out := messageSearchPage{Messages: make([]foundMessage, 0, len(outcome.page.Found)), Total: outcome.page.Total}
	for _, f := range outcome.page.Found {
		out.Messages = append(out.Messages, foundMessage{
			ID:             f.Message.ID,
			AccountID:      f.Message.AccountID,
			Date:           f.Message.Date,
			From:           mailAddressesOf(f.Message.From),
			Subject:        f.Message.Subject,
			SubjectMatches: textMatchesOf(f.Message.Subject, f.SubjectMatches),
			Preview:        f.Preview,
			PreviewMatches: textMatchesOf(f.Preview, f.PreviewMatches),
			Unread:         f.Message.Unread,
			Starred:        f.Message.Starred,
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

// utf16Length returns how many UTF-16 code units text takes: two for a
// character past U+FFFF, e.g. most emoji, and one for any other.
func utf16Length(text string) int {
	n := 0
	for _, r := range text {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// searchProblem maps the errors of a search.
func searchProblem(err error) error {
	switch {
	case errors.Is(err, search.ErrAccountNotFound):
		return connectedAccountProblem(connectedaccount.ErrNotFound)
	case errors.Is(err, search.ErrUnavailable):
		return huma.ErrorWithHeaders(searchUnavailableProblem(), http.Header{retryAfterHeader: {retryAfterSeconds(searchUnavailableRetry)}})
	default:
		return err
	}
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
