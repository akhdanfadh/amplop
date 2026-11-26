package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/account"
	"amplop/internal/conversation"
	"amplop/internal/mailsync"
	"amplop/internal/search"
	"amplop/internal/session"
)

// fakeSearches answers with page or err, or panics with panicWith when it
// is set, and records what it was asked.
// When block is set, each search first says so on started, then waits
// until block is closed or its context ends.
type fakeSearches struct {
	page      search.Page
	err       error
	block     chan struct{}
	started   chan struct{}
	panicWith any

	mu      sync.Mutex
	called  bool
	owner   int64
	request search.Request
}

func (f *fakeSearches) Search(ctx context.Context, owner int64, request search.Request) (search.Page, error) {
	f.mu.Lock()
	f.called, f.owner, f.request = true, owner, request
	f.mu.Unlock()
	if f.block != nil {
		f.started <- struct{}{}
		select {
		case <-f.block:
		case <-ctx.Done():
			return search.Page{}, ctx.Err()
		}
	}
	if f.panicWith != nil {
		panic(f.panicWith)
	}
	return f.page, f.err
}

// searchAPI returns the API searching with searches for a signed-in
// member, whose session may be read by several requests at once.
func searchAPI(searches *fakeSearches) *API {
	return New(discard, Services{Search: searches, Session: &lockedSessions{fakeSessions: memberSessions(account.RoleMember)}})
}

// lockedSessions is [fakeSessions] whose Current, which records each call,
// is safe for concurrent requests.
type lockedSessions struct {
	*fakeSessions
	mu sync.Mutex
}

func (l *lockedSessions) Current(ctx context.Context, secret string) (session.Authenticated, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeSessions.Current(ctx, secret)
}

// A search answers each result as a list row shows it, with the message it
// matched through, its matches in UTF-16 code units, and the total; the
// signed-in member is the owner.
func TestSearchConversations(t *testing.T) {
	id := uuid.MustParse("0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0b")
	conversationID := uuid.MustParse("0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0c")
	label := uuid.MustParse("0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0d")
	// é is two bytes and one unit; 😀 four bytes and two units.
	subject := "Café 😀 invoice"
	preview := "…pay the invoice"
	budi := []mailsync.Address{{Name: "Budi", Address: "budi@example.net"}}
	searches := &fakeSearches{page: search.Page{Total: 37, Found: []search.Found{{
		Conversation: conversation.Conversation{
			ID:           conversationID,
			AccountID:    3,
			LatestDate:   time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC),
			Subject:      "Invoice",
			Preview:      "Thanks",
			LatestFrom:   budi,
			Participants: budi,
			MessageCount: 2,
			Unread:       true,
			InInbox:      true,
			InAllMail:    true,
			LabelIDs:     []uuid.UUID{label},
		},
		Message: search.Message{
			ID:        id,
			AccountID: 3,
			Date:      time.Date(2026, 11, 2, 9, 14, 0, 0, time.UTC),
			From:      budi,
			Subject:   subject,
			Preview:   "Gmail's snippet",
		},
		SubjectMatches: []search.Span{{Start: strings.Index(subject, "invoice"), Length: 7}},
		Preview:        preview,
		PreviewMatches: []search.Span{{Start: strings.Index(preview, "invoice"), Length: 7}},
	}}}}
	api := searchAPI(searches)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=invoce&account=3&page=2&limit=5", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"data":{"total":37,"results":[{
		"conversation":{
			"id":"0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0c","accountId":3,"date":"2026-11-03T08:00:00Z",
			"subject":"Invoice","preview":"Thanks",
			"latestFrom":[{"name":"Budi","address":"budi@example.net"}],
			"participants":[{"name":"Budi","address":"budi@example.net"}],
			"messageCount":2,"unread":true,"starred":false,
			"inbox":true,"sent":false,"allMail":true,"spam":false,"trash":false,
			"labelIds":["0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0d"]
		},
		"match":{
			"messageId":"0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0b","date":"2026-11-02T09:14:00Z",
			"from":[{"name":"Budi","address":"budi@example.net"}],
			"subject":"Café 😀 invoice","subjectMatches":[{"start":8,"length":7}],
			"preview":"…pay the invoice","previewMatches":[{"start":9,"length":7}]
		}
	}]}}`, response.Body.String())
	assert.Equal(t, int64(42), searches.owner)
	assert.Equal(t, search.Request{Text: "invoce", Account: 3, Page: 2, Limit: 5}, searches.request)
}

// Each place reaches the search, in order: views by name, labels by id.
func TestSearchConversationsPlaces(t *testing.T) {
	label := uuid.MustParse("0190a5f6-1c2d-7e3f-8a4b-5c6d7e8f9a0d")
	searches := &fakeSearches{}
	api := searchAPI(searches)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a&place=spam&place="+label.String()+"&place=all", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, []conversation.Place{{View: conversation.ViewSpam}, {Mailbox: label}, {View: conversation.ViewAll}}, searches.request.Places)
}

// A place that is neither a view nor a mailbox id is refused where it
// stands, an empty first one too, whose parameter Huma would otherwise
// skip, and so are more than 20.
func TestSearchConversationsInvalidPlaces(t *testing.T) {
	cases := map[string]string{
		"place=inbox&place=drafts":     "query.place[1]",
		"place=&place=inbox":           "query.place[0]",
		"place=Inbox":                  "query.place[0]",
		"place=inbox&place=label-name": "query.place[1]",
	}
	for query, location := range cases {
		searches := &fakeSearches{}
		api := searchAPI(searches)

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, query)
		assert.Contains(t, response.Body.String(), `{"location":"`+location+`","message":"Choose a view or one of your labels."}`, query)
		assert.False(t, searches.called, query)
	}

	searches := &fakeSearches{}
	tooMany := requestAuthenticated(searchAPI(searches), http.MethodGet, "/conversations/search?"+strings.Repeat("place=inbox&", 21), "")
	twenty := requestAuthenticated(searchAPI(&fakeSearches{}), http.MethodGet, "/conversations/search?"+strings.Repeat("place=inbox&", 20), "")

	assert.Equal(t, http.StatusUnprocessableEntity, tooMany.Code)
	assert.False(t, searches.called)
	assert.Equal(t, http.StatusOK, twenty.Code)
}

// Without paging parameters a search asks for the first page of 50, every
// account, and lists empty matches as empty lists.
func TestSearchConversationsDefaults(t *testing.T) {
	searches := &fakeSearches{page: search.Page{Found: []search.Found{{Message: search.Message{Subject: "Hello"}, Preview: "Hi"}}, Total: 1}}
	api := searchAPI(searches)

	response := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=hello", "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, search.Request{Text: "hello", Page: 1, Limit: 50}, searches.request)
	assert.Contains(t, response.Body.String(), `"from":[],"subject":"Hello","subjectMatches":[],"preview":"Hi","previewMatches":[]`)
	assert.Contains(t, response.Body.String(), `"latestFrom":[],"participants":[]`)
}

// Each filter reaches the search: senders repeated, instants with their
// offsets, and has; with no words, the search has none.
func TestSearchConversationsFilters(t *testing.T) {
	searches := &fakeSearches{}
	api := searchAPI(searches)
	query := "from=budi%40example.net&from=Asha.K%40example.com" +
		"&since=2026-11-01T00%3A00%3A00%2B07%3A00&before=2026-12-01T00%3A00%3A00.5%2B07%3A00&has=attachment"

	response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	jakarta := time.FixedZone("", 7*60*60)
	want := search.Request{
		From:           []string{"budi@example.net", "Asha.K@example.com"},
		Since:          time.Date(2026, 11, 1, 0, 0, 0, 0, jakarta),
		Before:         time.Date(2026, 12, 1, 0, 0, 0, 500_000_000, jakarta),
		WithAttachment: true,
		Page:           1,
		Limit:          50,
	}
	got := searches.request
	// The same instants, whatever location the parser gave them.
	assert.True(t, want.Since.Equal(got.Since), "since %v", got.Since)
	assert.True(t, want.Before.Equal(got.Before), "before %v", got.Before)
	got.Since, got.Before = want.Since, want.Before
	assert.Equal(t, want, got)
}

// A search with nothing but paging finds every message, newest first: the
// search gets no words and no filters.
func TestSearchConversationsNothing(t *testing.T) {
	for _, query := range []string{"", "q=", "since=&before=&has="} {
		searches := &fakeSearches{}
		api := searchAPI(searches)

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")

		require.Equal(t, http.StatusOK, response.Code, query)
		assert.Equal(t, search.Request{Page: 1, Limit: 50}, searches.request, query)
	}
}

// A range that holds nothing is refused on before, the parameter the
// member would change, in words that name no parameter, before searching
// or taking a place: with two searches running, it is still a 422.
func TestSearchConversationsBadRange(t *testing.T) {
	for name, query := range map[string]string{
		"swapped": "since=2026-11-02T00%3A00%3A00Z&before=2026-11-01T00%3A00%3A00Z",
		"equal":   "since=2026-11-01T07%3A00%3A00%2B07%3A00&before=2026-11-01T00%3A00%3A00Z",
	} {
		searches := &fakeSearches{block: make(chan struct{}), started: make(chan struct{}, 2)}
		api := searchAPI(searches)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "") })
			<-searches.started
		}

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")
		close(searches.block)
		wg.Wait()

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, name)
		assert.Contains(t, response.Body.String(), `"errors":[{"location":"query.before","message":"Choose an end after the start."}]`, name)
		assert.NotContains(t, response.Body.String(), "2026", name)
	}
}

// Text that is blank or too long, filters out of their limits, and paging
// out of range, are refused before searching.
func TestSearchConversationsInvalid(t *testing.T) {
	cases := map[string]string{
		"blank":      "q=%20%20",
		"too long":   "q=" + strings.Repeat("a", 201),
		"page 0":     "q=a&page=0",
		"page 1001":  "q=a&page=1001",
		"limit 0":    "q=a&limit=0",
		"limit 51":   "q=a&limit=51",
		"account 0":  "q=a&account=0",
		"not a page": "q=a&page=two",
		"blank from": "from=%20",
		// Huma skips a parameter whose first value is empty, checks and all.
		"empty from":       "from=",
		"empty first from": "from=&from=a%40example.com",
		"blank last from":  "from=a%40example.com&from=%20",
		"empty first, 11":  "from=&" + strings.Repeat("from=a%40example.com&", 11),
		"zero before":      "before=0001-01-01T00%3A00%3A00Z",
		"zero since":       "since=0001-01-01T00%3A00%3A00Z",
		"long from":        "from=" + longAddress + "a",
		"not an address":   "from=amazon",
		// Huma's check wants the address net/mail reads, which drops the
		// quotes: a limit amplop accepts.
		"quoted local part": "from=%22a%2Cb%22%40example.com",
		"11 senders":        strings.Repeat("from=a%40example.com&", 11),
		"other has":         "has=star",
		"a date":            "since=2026-11-01",
		"no offset":         "since=2026-11-01T00%3A00%3A00",
	}
	for name, query := range cases {
		searches := &fakeSearches{}
		api := searchAPI(searches)

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, name)
		assert.False(t, searches.called, name)
	}
}

// A blank sender is refused where it stands as no address: an empty
// first one, whose parameter Huma would otherwise skip, and a blank one
// later, which Huma checks itself.
func TestSearchConversationsBlankSender(t *testing.T) {
	cases := map[string]string{
		"from=&from=a%40example.com&from=%20": "query.from[0]",
		"from=a%40example.com&from=%20":       "query.from[1]",
		"from=%20&from=a%40example.com":       "query.from[0]",
	}
	for query, location := range cases {
		searches := &fakeSearches{}
		api := searchAPI(searches)

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, query)
		assert.Contains(t, response.Body.String(), `"errors":[{"location":"`+location+`","message":"Enter an email address, like name@example.com."}]`, query)
		assert.False(t, searches.called, query)
	}
}

// longAddress is an email address of 254 characters, the most a sender
// may have.
var longAddress = "a@" + strings.Repeat("b", 248) + ".com"

// Text of exactly 200 characters, not bytes, is searched, and so are 10
// senders of 254 characters each.
func TestSearchConversationsLongest(t *testing.T) {
	for name, query := range map[string]string{
		"text":    "q=" + strings.Repeat("é", 200),
		"senders": strings.Repeat("from="+longAddress+"&", 10),
	} {
		searches := &fakeSearches{}
		api := searchAPI(searches)

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?"+query, "")

		require.Equal(t, http.StatusOK, response.Code, name)
		assert.True(t, searches.called, name)
	}
}

// A search's failures answer their problems: an account or a label the
// member does not have is 404, as for a list, and an index that does not
// answer is 503 with Retry-After.
func TestSearchConversationsProblems(t *testing.T) {
	cases := map[string]struct {
		err        error
		status     int
		problem    string
		retryAfter string
	}{
		"account": {search.ErrAccountNotFound, http.StatusNotFound, connectedAccountNotFoundType, ""},
		"label":   {conversation.ErrLabelNotFound, http.StatusNotFound, labelNotFoundType, ""},
		"index":   {fmt.Errorf("search: %w", search.ErrUnavailable), http.StatusServiceUnavailable, searchUnavailableType, "15"},
		"range":   {search.ErrBadRange, http.StatusUnprocessableEntity, "validation-error", ""},
		"other":   {errors.New("database down"), http.StatusInternalServerError, "", ""},
	}
	for name, c := range cases {
		api := searchAPI(&fakeSearches{err: c.err})

		response := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=invoice", "")

		assert.Equal(t, c.status, response.Code, name)
		if c.problem != "" {
			assert.Contains(t, response.Body.String(), c.problem, name)
		}
		assert.Equal(t, c.retryAfter, response.Header().Get(retryAfterHeader), name)
	}
}

// A member may run two searches at once; a third is refused with 429 and
// Retry-After before it reaches the index, until one ends.
func TestSearchConversationsRunning(t *testing.T) {
	searches := &fakeSearches{block: make(chan struct{}), started: make(chan struct{}, 3)}
	api := searchAPI(searches)
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { codes <- requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "").Code })
	}
	<-searches.started
	<-searches.started

	third := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "")
	close(searches.block)
	wg.Wait()
	close(codes)
	after := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "")

	assert.Equal(t, http.StatusTooManyRequests, third.Code)
	assert.Equal(t, "1", third.Header().Get(retryAfterHeader))
	assert.Contains(t, third.Body.String(), searchesRunningType)
	for code := range codes {
		assert.Equal(t, http.StatusOK, code)
	}
	assert.Equal(t, http.StatusOK, after.Code)
}

// A search whose client gave up runs on, apart from the request, and keeps
// its place until it ends, so cancelling and sending again never runs more
// than two at once.
func TestSearchConversationsCancelled(t *testing.T) {
	searches := &fakeSearches{block: make(chan struct{}), started: make(chan struct{}, 3)}
	api := searchAPI(searches)
	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v0/conversations/search?q=a", nil)
	request.Header.Set("Origin", "http://"+request.Host)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	gaveUp := make(chan struct{})
	go func() {
		api.Handler().ServeHTTP(httptest.NewRecorder(), request)
		close(gaveUp)
	}()
	<-searches.started
	cancel()
	<-gaveUp
	var wg sync.WaitGroup
	wg.Go(func() { requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "") })
	<-searches.started

	third := requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "")
	close(searches.block)
	wg.Wait()

	assert.Equal(t, http.StatusTooManyRequests, third.Code)
}

// A search that panics answers 500 and gives its place back, and the
// process goes on.
func TestSearchConversationsPanics(t *testing.T) {
	searches := &fakeSearches{panicWith: "index client bug"}
	api := searchAPI(searches)

	codes := []int{}
	for range maxRunningSearches + 1 {
		codes = append(codes, requestAuthenticated(api, http.MethodGet, "/conversations/search?q=a", "").Code)
	}

	assert.Equal(t, []int{500, 500, 500}, codes)
}

// Each member's searches count apart, and an ended search gives its place
// back.
func TestRunningSearches(t *testing.T) {
	running := newRunningSearches(2)

	first, second, third := running.take(1), running.take(1), running.take(1)
	other := running.take(2)
	running.release(1)
	again := running.take(1)
	running.release(1)
	running.release(1)
	running.release(2)

	assert.Equal(t, []bool{true, true, false, true, true}, []bool{first, second, third, other, again})
	assert.Empty(t, running.members)
}

// The page and total limits in the contract are the most matches a search
// counts.
func TestSearchConversationsBounds(t *testing.T) {
	doc := searchAPI(&fakeSearches{}).api.OpenAPI()
	operation := doc.Paths["/conversations/search"].Get
	var page *huma.Param
	for _, p := range operation.Parameters {
		if p.Name == "page" {
			page = p
		}
	}
	total := doc.Components.Schemas.Map()["ConversationSearchPage"].Properties["total"]

	require.NotNil(t, page)
	assert.InDelta(t, float64(search.MaxTotal), *page.Schema.Maximum, 0)
	assert.InDelta(t, float64(search.MaxTotal), *total.Maximum, 0)
}

// Byte offsets become UTF-16 code units: one for a character up to U+FFFF,
// two past it.
func TestTextMatchesOf(t *testing.T) {
	text := "a é ✓ 😀 z"
	spans := []search.Span{
		{Start: strings.Index(text, "é"), Length: len("é")},
		{Start: strings.Index(text, "✓"), Length: len("✓")},
		{Start: strings.Index(text, "😀"), Length: len("😀")},
		{Start: strings.Index(text, "z"), Length: 1},
	}

	got := textMatchesOf(text, spans)

	assert.Equal(t, []textMatch{{Start: 2, Length: 1}, {Start: 4, Length: 1}, {Start: 6, Length: 2}, {Start: 9, Length: 1}}, got)
}
