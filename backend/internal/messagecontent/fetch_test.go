package messagecontent_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/connectedaccount"
	"amplop/internal/mailsync"
	"amplop/internal/messagecontent"
	"amplop/internal/messagecontent/messagecontenttest"
)

// fetchNow is the fetcher's clock in these tests.
var fetchNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

// fetchAccount is the account the tests fetch for.
const fetchAccount int64 = 7

// fakeProvider answers with messages and part contents by provider ID,
// or err for the IDs in errs, and counts its calls.
type fakeProvider struct {
	mu       sync.Mutex
	messages map[string]messagecontent.Received
	parts    map[string][]byte // by provider ID and part ID, as "m1/0.1"
	// converted holds the charset the provider converted a part's bytes
	// to, by the same key; none means as sent.
	converted map[string]string
	errs      map[string]error
	calls     []string
	// fetched, when set, is called with each message's provider ID as
	// it is fetched, e.g. to delete it meanwhile.
	fetched func(providerID string)
}

func (p *fakeProvider) Message(_ context.Context, _ connectedaccount.AccessToken, accountID int64, providerID string) (messagecontent.Received, error) {
	if p.fetched != nil {
		p.fetched(providerID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, providerID)
	if accountID != fetchAccount {
		return messagecontent.Received{}, messagecontent.ErrGone
	}
	if err := p.errs[providerID]; err != nil {
		return messagecontent.Received{}, err
	}
	received, ok := p.messages[providerID]
	if !ok {
		return messagecontent.Received{}, messagecontent.ErrGone
	}
	return received, nil
}

func (p *fakeProvider) PartContent(_ context.Context, _ connectedaccount.AccessToken, _ int64, providerID, partID string) ([]byte, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := providerID + "/" + partID
	p.calls = append(p.calls, key)
	if err := p.errs[key]; err != nil {
		return nil, "", err
	}
	content, ok := p.parts[key]
	if !ok {
		return nil, "", messagecontent.ErrGone
	}
	return content, p.converted[key], nil
}

// called returns the calls made so far.
func (p *fakeProvider) called() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// fakeAccess hands out a token, or err.
type fakeAccess struct{ err error }

func (a fakeAccess) Token(context.Context, int64) (connectedaccount.AccessToken, error) {
	if a.err != nil {
		return connectedaccount.AccessToken{}, a.err
	}
	return connectedaccount.NewAccessToken("access"), nil
}

// fetchMemory is a [partsMemory] that also knows each message's provider
// ID and failed fetch, and lists the messages it holds without parts in
// the order they were added, as the newest first.
type fetchMemory struct {
	*partsMemory
	order       []uuid.UUID
	providerIDs map[uuid.UUID]string
	failed      map[uuid.UUID]time.Time
	complete    bool
	filter      messagecontent.DueFilter // as last asked
}

// newFetchMemory returns storage holding a message of [fetchAccount] for
// each of providerIDs, newest first, none with parts, its full sync
// complete. It returns their IDs in the same order.
func newFetchMemory(providerIDs ...string) (*fetchMemory, []uuid.UUID) {
	m := &fetchMemory{partsMemory: newPartsMemory(fetchAccount), providerIDs: map[uuid.UUID]string{}, failed: map[uuid.UUID]time.Time{}, complete: true}
	ids := make([]uuid.UUID, len(providerIDs))
	for i, providerID := range providerIDs {
		ids[i] = uuid.New()
		m.messages[ids[i]] = fetchAccount
		m.providerIDs[ids[i]] = providerID
		m.order = append(m.order, ids[i])
	}
	return m, ids
}

// remove deletes message id, as synchronization would.
func (m *fetchMemory) remove(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.messages, id)
}

func (m *fetchMemory) AccountsWithDueBodies(ctx context.Context, filter messagecontent.DueFilter) ([]int64, error) {
	if !m.complete {
		return nil, nil
	}
	pending, err := m.MessagesNeedingBodies(ctx, fetchAccount, filter, 1)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	return []int64{fetchAccount}, nil
}

func (m *fetchMemory) MessagesNeedingBodies(_ context.Context, accountID int64, filter messagecontent.DueFilter, limit int) ([]messagecontent.PendingBody, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filter = filter
	var pending []messagecontent.PendingBody
	for _, id := range m.order {
		_, saved := m.trees[id]
		failed, ok := m.failed[id]
		if m.messages[id] != accountID || saved || (ok && !failed.Before(filter.FailedSince)) {
			continue
		}
		pending = append(pending, messagecontent.PendingBody{ID: id, ProviderID: m.providerIDs[id]})
		if len(pending) == limit {
			break
		}
	}
	return pending, nil
}

func (m *fetchMemory) MarkBodyFailed(_ context.Context, accountID int64, messageID uuid.UUID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.messages[messageID] != accountID {
		return messagecontent.ErrMessageNotFound
	}
	m.failed[messageID] = at
	return nil
}

func (m *fetchMemory) MessageProviderID(_ context.Context, accountID int64, messageID uuid.UUID) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.messages[messageID] != accountID {
		return "", messagecontent.ErrMessageNotFound
	}
	return m.providerIDs[messageID], nil
}

// alternative is a message from a provider with plain text and HTML.
func alternative(text string) messagecontent.Received {
	return messagecontent.Received{MediaType: "multipart/alternative", Header: textproto.MIMEHeader{}, Parts: []messagecontent.Received{
		{ID: "0", MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte(text)},
		{ID: "1", MediaType: "text/html", Header: textproto.MIMEHeader{}, Content: []byte("<p>" + text + "</p>")},
	}}
}

// fetchStack is a fetcher with its fakes.
type fetchStack struct {
	fetcher  *messagecontent.Fetcher
	store    *messagecontenttest.Fake
	storage  *fetchMemory
	provider *fakeProvider
	logs     *bytes.Buffer
}

// newFetchStack returns a fetcher over storage, answering with provider.
func newFetchStack(storage *fetchMemory, provider *fakeProvider, access fakeAccess) fetchStack {
	store := &messagecontenttest.Fake{}
	logs := &bytes.Buffer{}
	fetcher := messagecontent.NewFetcher(messagecontent.NewContents(store, storage), storage, provider, access,
		func() time.Time { return fetchNow }, slog.New(slog.NewTextHandler(logs, nil)))
	return fetchStack{fetcher: fetcher, store: store, storage: storage, provider: provider, logs: logs}
}

// texts returns the stored text of message id's parts, by part ID.
func (s fetchStack) texts(t *testing.T, id uuid.UUID) map[string]string {
	t.Helper()
	root, ok, err := s.storage.MessageParts(t.Context(), fetchAccount, id)
	require.NoError(t, err)
	require.True(t, ok, "the message has parts")
	texts := map[string]string{}
	for _, part := range flatten(root) {
		if part.BlobID != uuid.Nil() {
			texts[part.ID] = stored(t, s.store, messagecontent.Ref{Account: fetchAccount, ID: part.BlobID})
		}
	}
	return texts
}

// The due messages' content is fetched, newest first, and saved; the
// window is the last 90 days, and failures are skipped for a day.
func TestFetchDue(t *testing.T) {
	storage, ids := newFetchMemory("m1", "m2")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": alternative("one"), "m2": alternative("two")}}
	s := newFetchStack(storage, provider, fakeAccess{})

	batch, err := s.fetcher.FetchDue(t.Context(), fetchAccount)

	require.NoError(t, err)
	assert.Equal(t, messagecontent.Batch{Saved: 2}, batch)
	assert.Equal(t, []string{"m1", "m2"}, provider.called())
	assert.Equal(t, map[string]string{"0": "one", "1": "<p>one</p>"}, s.texts(t, ids[0]))
	assert.Equal(t, map[string]string{"0": "two", "1": "<p>two</p>"}, s.texts(t, ids[1]))
	assert.Equal(t, messagecontent.DueFilter{
		Since:         fetchNow.Add(-mailsync.Window),
		FailedSince:   fetchNow.Add(-24 * time.Hour),
		ExcludedRoles: []mailsync.Role{mailsync.RoleJunk, mailsync.RoleTrash},
	}, storage.filter)
}

// A batch takes 20 messages and says more may be due.
func TestFetchDueMore(t *testing.T) {
	providerIDs := make([]string, 25)
	provider := &fakeProvider{messages: map[string]messagecontent.Received{}}
	for i := range providerIDs {
		providerIDs[i] = fmt.Sprintf("m%02d", i)
		provider.messages[providerIDs[i]] = alternative("hi")
	}
	storage, _ := newFetchMemory(providerIDs...)
	s := newFetchStack(storage, provider, fakeAccess{})

	first, err := s.fetcher.FetchDue(t.Context(), fetchAccount)
	require.NoError(t, err)
	second, err := s.fetcher.FetchDue(t.Context(), fetchAccount)
	require.NoError(t, err)

	assert.Equal(t, messagecontent.Batch{Saved: 20, More: true}, first)
	assert.Equal(t, messagecontent.Batch{Saved: 5}, second)
}

// A message whose fetch fails for good is recorded, logged, and skipped,
// and the batch goes on.
func TestFetchDueFailsForGood(t *testing.T) {
	tooMany := messagecontent.Received{MediaType: "multipart/mixed", Header: textproto.MIMEHeader{}}
	for range 1001 {
		tooMany.Parts = append(tooMany.Parts, messagecontent.Received{MediaType: "text/plain", Header: textproto.MIMEHeader{}})
	}
	storage, ids := newFetchMemory("gone", "large", "unreadable", "parts", "ok")
	provider := &fakeProvider{
		messages: map[string]messagecontent.Received{"parts": tooMany, "ok": alternative("hi")},
		errs: map[string]error{
			"gone":       messagecontent.ErrGone,
			"large":      fmt.Errorf("fetch: %w", messagecontent.ErrTooLarge),
			"unreadable": messagecontent.ErrUnreadable,
		},
	}
	s := newFetchStack(storage, provider, fakeAccess{})

	batch, err := s.fetcher.FetchDue(t.Context(), fetchAccount)

	require.NoError(t, err)
	assert.Equal(t, messagecontent.Batch{Saved: 1, Failed: 4}, batch)
	for _, id := range ids[:4] {
		assert.Equal(t, fetchNow, storage.failed[id])
	}
	assert.Equal(t, 4, strings.Count(s.logs.String(), "message body fetch failed"))
	assert.Equal(t, map[string]string{"0": "hi", "1": "<p>hi</p>"}, s.texts(t, ids[4]))

	// The next batch skips them for a day.
	again, err := s.fetcher.FetchDue(t.Context(), fetchAccount)
	require.NoError(t, err)
	assert.Equal(t, messagecontent.Batch{}, again)
}

// A message synchronization deletes while it is fetched is passed over,
// neither saved nor marked, and the batch goes on.
func TestFetchDueDeletedMeanwhile(t *testing.T) {
	storage, ids := newFetchMemory("deleted", "ok")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"deleted": alternative("bye"), "ok": alternative("hi")}}
	provider.fetched = func(providerID string) {
		if providerID == "deleted" {
			storage.remove(ids[0])
		}
	}
	s := newFetchStack(storage, provider, fakeAccess{})

	batch, err := s.fetcher.FetchDue(t.Context(), fetchAccount)

	require.NoError(t, err)
	assert.Equal(t, messagecontent.Batch{Saved: 1}, batch)
	assert.Equal(t, []string{"deleted", "ok"}, provider.called())
	assert.Empty(t, storage.failed)
	_, saved, err := storage.MessageParts(t.Context(), fetchAccount, ids[0])
	require.NoError(t, err)
	assert.False(t, saved)
	assert.Equal(t, map[string]string{"0": "hi", "1": "<p>hi</p>"}, s.texts(t, ids[1]))
}

// Any other failure ends the batch with it, keeping what it saved.
func TestFetchDueFails(t *testing.T) {
	busy := errors.New("provider busy")
	storage, ids := newFetchMemory("m1", "m2", "m3")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": alternative("one"), "m3": alternative("three")}, errs: map[string]error{"m2": busy}}
	s := newFetchStack(storage, provider, fakeAccess{})

	batch, err := s.fetcher.FetchDue(t.Context(), fetchAccount)

	require.ErrorIs(t, err, busy)
	assert.Equal(t, messagecontent.Batch{Saved: 1}, batch)
	assert.Empty(t, storage.failed)
	assert.Equal(t, []string{"m1", "m2"}, provider.called())
	_, ok, err := storage.MessageParts(t.Context(), fetchAccount, ids[2])
	require.NoError(t, err)
	assert.False(t, ok)
}

// An account whose provider work has stopped asks the provider nothing.
func TestFetchDueStopped(t *testing.T) {
	storage, _ := newFetchMemory("m1")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": alternative("one")}}
	s := newFetchStack(storage, provider, fakeAccess{err: connectedaccount.ErrNeedsReconnect})

	_, err := s.fetcher.FetchDue(t.Context(), fetchAccount)

	require.ErrorIs(t, err, connectedaccount.ErrNeedsReconnect)
	assert.Empty(t, provider.called())
}

// An account's bodies are due only once its full sync is complete, and
// while a message needs one, counted over the last 90 days.
func TestDueAccounts(t *testing.T) {
	storage, ids := newFetchMemory("m1")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": alternative("one")}}
	s := newFetchStack(storage, provider, fakeAccess{})

	storage.complete = false
	accounts, err := s.fetcher.DueAccounts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, accounts, "during the full sync")

	storage.complete = true
	accounts, err = s.fetcher.DueAccounts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []int64{fetchAccount}, accounts)
	assert.Equal(t, messagecontent.DueFilter{
		Since:         fetchNow.Add(-mailsync.Window),
		FailedSince:   fetchNow.Add(-24 * time.Hour),
		ExcludedRoles: []mailsync.Role{mailsync.RoleJunk, mailsync.RoleTrash},
	}, storage.filter)

	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))
	accounts, err = s.fetcher.DueAccounts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, accounts, "once every message has its body")
}

// A text part the provider sends separately is fetched by its part ID and
// converted to UTF-8, so the message shows its text.
func TestFetchSeparateText(t *testing.T) {
	latin1 := textproto.MIMEHeader{"Content-Type": {"text/html; charset=iso-8859-1"}}
	storage, ids := newFetchMemory("m1")
	provider := &fakeProvider{
		messages: map[string]messagecontent.Received{"m1": {MediaType: "multipart/alternative", Header: textproto.MIMEHeader{}, Parts: []messagecontent.Received{
			{ID: "0", MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte("Caf")},
			{ID: "1", MediaType: "text/html", Header: latin1},
		}}},
		parts: map[string][]byte{"m1/1": []byte("<p>Caf\xe9</p>")},
	}
	s := newFetchStack(storage, provider, fakeAccess{})

	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))

	assert.Equal(t, map[string]string{"0": "Caf", "1": "<p>Café</p>"}, s.texts(t, ids[0]))
	assert.Equal(t, []string{"m1", "m1/1"}, provider.called())
}

// Opening fetches any message, even one whose fetch failed, and fetching
// a saved one again restores text the store lost under the same IDs.
func TestFetchMessage(t *testing.T) {
	storage, ids := newFetchMemory("m1")
	storage.failed[ids[0]] = fetchNow
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": alternative("one")}}
	s := newFetchStack(storage, provider, fakeAccess{})

	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))
	root, _, err := storage.MessageParts(t.Context(), fetchAccount, ids[0])
	require.NoError(t, err)
	lost := messagecontent.Ref{Account: fetchAccount, ID: root.Parts[0].BlobID}
	require.NoError(t, s.store.Delete(t.Context(), []messagecontent.Ref{lost}))

	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))

	assert.Equal(t, "one", stored(t, s.store, lost))
	err = s.fetcher.FetchMessage(t.Context(), fetchAccount+1, ids[0])
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
}

// A part's content is fetched on request, stored, and named in its row; a
// text part's is converted to UTF-8, and fetching again keeps its ID.
func TestFetchPart(t *testing.T) {
	latin1 := textproto.MIMEHeader{"Content-Type": {"text/plain; charset=iso-8859-1"}}
	attachment := textproto.MIMEHeader{"Content-Disposition": {`attachment; filename="report.pdf"`}}
	storage, ids := newFetchMemory("m1")
	provider := &fakeProvider{
		messages: map[string]messagecontent.Received{"m1": {MediaType: "multipart/mixed", Header: textproto.MIMEHeader{}, Parts: []messagecontent.Received{
			{ID: "0", MediaType: "text/plain", Header: latin1},
			{ID: "1", MediaType: "application/pdf", Filename: "report.pdf", Header: attachment},
		}}},
		parts: map[string][]byte{"m1/0": []byte("Caf\xe9"), "m1/1": []byte("%PDF\xff")},
	}
	s := newFetchStack(storage, provider, fakeAccess{})
	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))

	pdf, err := s.fetcher.FetchPart(t.Context(), fetchAccount, ids[0], []int{1})
	require.NoError(t, err)
	assert.Equal(t, "report.pdf", pdf.Filename)
	assert.Equal(t, "%PDF\xff", stored(t, s.store, messagecontent.Ref{Account: fetchAccount, ID: pdf.BlobID}))
	again, err := s.fetcher.FetchPart(t.Context(), fetchAccount, ids[0], []int{1})
	require.NoError(t, err)
	assert.Equal(t, pdf.BlobID, again.BlobID)

	text, err := s.fetcher.FetchPart(t.Context(), fetchAccount, ids[0], []int{0})
	require.NoError(t, err)
	assert.Equal(t, "Café", stored(t, s.store, messagecontent.Ref{Account: fetchAccount, ID: text.BlobID}))
	root, _, err := storage.MessageParts(t.Context(), fetchAccount, ids[0])
	require.NoError(t, err)
	assert.Equal(t, pdf.BlobID, root.Parts[1].BlobID)
	assert.Equal(t, text.BlobID, root.Parts[0].BlobID)
}

// Text the provider already converted, e.g. Gmail's inline data, is kept
// as it comes, whatever charset the part declares, in a message and when
// a part is fetched again.
func TestFetchConvertedText(t *testing.T) {
	latin1 := textproto.MIMEHeader{"Content-Type": {"text/plain; charset=iso-8859-1"}}
	storage, ids := newFetchMemory("m1")
	provider := &fakeProvider{
		messages: map[string]messagecontent.Received{"m1": {
			MediaType: "text/plain", Header: latin1, Content: []byte("Café"), ContentCharset: "utf-8",
		}},
		parts:     map[string][]byte{"m1/": []byte("Café")},
		converted: map[string]string{"m1/": "utf-8"},
	}
	s := newFetchStack(storage, provider, fakeAccess{})

	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))
	assert.Equal(t, map[string]string{"": "Café"}, s.texts(t, ids[0]))

	part, err := s.fetcher.FetchPart(t.Context(), fetchAccount, ids[0], []int{})
	require.NoError(t, err)
	assert.Equal(t, "Café", stored(t, s.store, messagecontent.Ref{Account: fetchAccount, ID: part.BlobID}))
}

// Only a saved part with content of its own is fetched.
func TestFetchPartNotFound(t *testing.T) {
	storage, ids := newFetchMemory("m1", "m2")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": alternative("one")}}
	s := newFetchStack(storage, provider, fakeAccess{})
	require.NoError(t, s.fetcher.FetchMessage(t.Context(), fetchAccount, ids[0]))

	for name, tc := range map[string]struct {
		message uuid.UUID
		path    []int
	}{
		"a multipart":  {message: ids[0], path: []int{}},
		"past the end": {message: ids[0], path: []int{2}},
		"negative":     {message: ids[0], path: []int{-1}},
		"below a text": {message: ids[0], path: []int{0, 0}},
		"no parts":     {message: ids[1], path: []int{0}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.fetcher.FetchPart(t.Context(), fetchAccount, tc.message, tc.path)
			require.ErrorIs(t, err, messagecontent.ErrPartNotFound)
		})
	}
	_, err := s.fetcher.FetchPart(t.Context(), fetchAccount+1, ids[0], []int{0})
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
}

// Saving a part whose row names a blob already, e.g. from a save that won
// a race, keeps that blob and returns it.
func TestContentsSavePartKeeps(t *testing.T) {
	message := uuid.New()
	storage := newPartsMemory(fetchAccount, message)
	contents := messagecontent.NewContents(&messagecontenttest.Fake{}, storage)
	require.NoError(t, contents.Save(t.Context(), fetchAccount, message, newsletter(), newsletterTexts))
	first, err := contents.SavePart(t.Context(), fetchAccount, message, []int{1}, newsletter().Parts[1], []byte("one"))
	require.NoError(t, err)

	second, err := contents.SavePart(t.Context(), fetchAccount, message, []int{1}, newsletter().Parts[1], []byte("two"))

	require.NoError(t, err)
	assert.Equal(t, first.BlobID, second.BlobID)
}
