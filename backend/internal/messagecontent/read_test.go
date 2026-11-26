package messagecontent_test

import (
	"context"
	"fmt"
	"io"
	"net/textproto"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/messagecontent"
)

// readUser is the user whose [fetchAccount] holds the messages.
const readUser int64 = 3

// readMemory is a [fetchMemory] whose messages all belong to [readUser].
type readMemory struct {
	*fetchMemory
}

func (m readMemory) UserMessageAccount(_ context.Context, userID int64, messageID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	accountID, ok := m.messages[messageID]
	if !ok || userID != readUser {
		return 0, messagecontent.ErrMessageNotFound
	}
	return accountID, nil
}

// readStack is a reader with its fakes.
type readStack struct {
	fetchStack
	reader *messagecontent.Reader
}

// newReadStack returns a reader of a message with provider ID "m1",
// whose provider sends message, and the message's ID.
func newReadStack(message messagecontent.Received, parts map[string][]byte) (readStack, uuid.UUID) {
	storage, ids := newFetchMemory("m1")
	provider := &fakeProvider{messages: map[string]messagecontent.Received{"m1": message}, parts: parts}
	s := newFetchStack(storage, provider, fakeAccess{})
	return readStack{fetchStack: s, reader: messagecontent.NewReader(s.store, readMemory{storage}, s.fetcher)}, ids[0]
}

// partURL writes a part's path into its URL, as "/parts/0.1".
func partURL(path []int) string {
	text := "0"
	for _, i := range path {
		text += fmt.Sprintf(".%d", i)
	}
	return "/parts/" + text
}

// related is a message whose HTML shows an inline image and a remote one,
// with another inline image it never shows.
func related() messagecontent.Received {
	html := `<p>Hi <img src="cid:logo"> <img src="https://tracker.example/p.gif"></p>`
	return messagecontent.Received{MediaType: "multipart/related", Header: textproto.MIMEHeader{}, Parts: []messagecontent.Received{
		{ID: "0", MediaType: "text/html", Header: textproto.MIMEHeader{}, Content: []byte(html)},
		{ID: "1", MediaType: "image/png", Header: textproto.MIMEHeader{"Content-Id": {"<logo>"}}},
		{ID: "2", MediaType: "image/gif", Header: textproto.MIMEHeader{"Content-Id": {"<unused>"}}},
	}}
}

// A message never fetched is fetched on its first view, then read from
// what is stored; its HTML body is learned, not sent.
func TestReadView(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)

	view, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	require.Len(t, view.Bodies, 1)
	assert.Equal(t, "1", view.Bodies[0].Part.ID)
	assert.Equal(t, []int{1}, view.Bodies[0].Path)
	assert.Nil(t, view.Bodies[0].Text)
	assert.False(t, view.Blocked)
	assert.Empty(t, view.Downloads)

	again, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	assert.Equal(t, view, again)
	assert.Equal(t, []string{"m1"}, s.provider.called(), "fetched once")
}

// Remote images are found, and an inline image the HTML never shows is
// offered as a download with its place.
func TestReadViewRelated(t *testing.T) {
	s, id := newReadStack(related(), nil)

	view, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	assert.True(t, view.Blocked)
	require.Len(t, view.Downloads, 1)
	assert.Equal(t, "2", view.Downloads[0].Part.ID)
	assert.Equal(t, []int{2}, view.Downloads[0].Path)
}

// Plain text past the clip comes cut at a character boundary, and says so.
func TestReadViewClipped(t *testing.T) {
	long := strings.Repeat("é", 60<<10) // 120 KiB
	message := messagecontent.Received{ID: "", MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte(long)}
	s, id := newReadStack(message, nil)

	view, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	require.Len(t, view.Bodies, 1)
	body := view.Bodies[0]
	assert.Equal(t, []int{}, body.Path)
	assert.True(t, body.Clipped)
	assert.LessOrEqual(t, len(body.Text), 102<<10)
	assert.Greater(t, len(body.Text), 101<<10)
	assert.True(t, utf8.Valid(body.Text))
	assert.True(t, strings.HasPrefix(long, string(body.Text)))
}

// Short plain text comes whole.
func TestReadViewText(t *testing.T) {
	message := messagecontent.Received{MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte("Hi")}
	s, id := newReadStack(message, nil)

	view, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	require.Len(t, view.Bodies, 1)
	assert.Equal(t, "Hi", string(view.Bodies[0].Text))
	assert.False(t, view.Bodies[0].Clipped)
}

// Another user's message, or one there is not, is not found, and nothing
// is fetched.
func TestReadViewNotFound(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)

	_, err := s.reader.View(t.Context(), readUser+1, id, partURL)
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
	_, err = s.reader.View(t.Context(), readUser, uuid.New(), partURL)
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
	_, _, err = s.reader.Part(t.Context(), readUser+1, id, []int{0})
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
	_, err = s.reader.Body(t.Context(), readUser+1, id, []int{1}, partURL)
	require.ErrorIs(t, err, messagecontent.ErrMessageNotFound)
	assert.Empty(t, s.provider.called())
}

// Text the store lost is fetched again and stored under its ID.
func TestReadViewLost(t *testing.T) {
	message := messagecontent.Received{MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte("Hi")}
	s, id := newReadStack(message, nil)
	_, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	root, _, err := s.storage.MessageParts(t.Context(), fetchAccount, id)
	require.NoError(t, err)
	require.NoError(t, s.store.DeleteAccount(t.Context(), fetchAccount))

	view, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	assert.Equal(t, "Hi", string(view.Bodies[0].Text))
	assert.Equal(t, "Hi", stored(t, s.store, messagecontent.Ref{Account: fetchAccount, ID: root.BlobID}))
	assert.Equal(t, []string{"m1", "m1"}, s.provider.called())
}

// A provider's failure ends the view with its error.
func TestReadViewFails(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)
	s.provider.errs = map[string]error{"m1": messagecontent.ErrGone}

	_, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.ErrorIs(t, err, messagecontent.ErrGone)
}

// An HTML body comes with the policy that shows its inline images at
// their part URLs; any other part is not a body.
func TestReadBody(t *testing.T) {
	s, id := newReadStack(related(), nil)

	body, err := s.reader.Body(t.Context(), readUser, id, []int{0}, partURL)
	require.NoError(t, err)
	content, err := io.ReadAll(body.Content)
	require.NoError(t, err)
	require.NoError(t, body.Content.Close())
	assert.Contains(t, string(content), "cid:logo")
	require.Len(t, body.Policy.Inline, 2)
	assert.Equal(t, "/parts/0.1", body.Policy.PartURL("1"))

	for _, path := range [][]int{{}, {1}, {9}} {
		_, err := s.reader.Body(t.Context(), readUser, id, path, partURL)
		require.ErrorIs(t, err, messagecontent.ErrPartNotFound, "path %v", path)
	}
}

// HTML sent as an attachment is a download, never a body.
func TestReadBodyAttachment(t *testing.T) {
	message := messagecontent.Received{MediaType: "multipart/mixed", Header: textproto.MIMEHeader{}, Parts: []messagecontent.Received{
		{ID: "0", MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte("See attached")},
		{ID: "1", MediaType: "text/html", Filename: "page.html", Header: textproto.MIMEHeader{"Content-Disposition": {"attachment"}}},
	}}
	s, id := newReadStack(message, map[string][]byte{"m1/1": []byte("<p>page</p>")})
	_, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)

	_, err = s.reader.Body(t.Context(), readUser, id, []int{1}, partURL)
	require.ErrorIs(t, err, messagecontent.ErrPartNotFound)
	part, content, err := s.reader.Part(t.Context(), readUser, id, []int{1})
	require.NoError(t, err)
	defer content.Close()
	assert.Equal(t, messagecontent.KindAttachment, part.Kind)
}

// An attachment is fetched on its first download, then read from the
// store, and fetched again once the store lost it.
func TestReadPart(t *testing.T) {
	message := messagecontent.Received{MediaType: "multipart/mixed", Header: textproto.MIMEHeader{}, Parts: []messagecontent.Received{
		{ID: "0", MediaType: "text/plain", Header: textproto.MIMEHeader{}, Content: []byte("See attached")},
		{ID: "1", MediaType: "application/pdf", Filename: "report.pdf", Header: textproto.MIMEHeader{}},
	}}
	s, id := newReadStack(message, map[string][]byte{"m1/1": []byte("%PDF")})
	view, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	require.Len(t, view.Downloads, 1)
	path := view.Downloads[0].Path

	read := func() string {
		t.Helper()
		part, content, err := s.reader.Part(t.Context(), readUser, id, path)
		require.NoError(t, err)
		defer content.Close()
		assert.Equal(t, "report.pdf", part.Filename)
		bytes, err := io.ReadAll(content)
		require.NoError(t, err)
		return string(bytes)
	}
	assert.Equal(t, "%PDF", read())
	assert.Equal(t, "%PDF", read())
	assert.Equal(t, []string{"m1", "m1/1"}, s.provider.called(), "fetched once")

	require.NoError(t, s.store.DeleteAccount(t.Context(), fetchAccount))
	assert.Equal(t, "%PDF", read())
	assert.Equal(t, []string{"m1", "m1/1", "m1/1"}, s.provider.called())
}

// A part the view neither shows nor offers is not served: a multipart, an
// alternative not chosen, or no part at all.
func TestReadPartNotInView(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)
	_, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)

	for _, path := range [][]int{{}, {0}, {5}, {1, 0}} {
		_, _, err := s.reader.Part(t.Context(), readUser, id, path)
		require.ErrorIs(t, err, messagecontent.ErrPartNotFound, "path %v", path)
	}
	_, content, err := s.reader.Part(t.Context(), readUser, id, []int{1})
	require.NoError(t, err)
	require.NoError(t, content.Close())
}

// A part of a message whose parts are not saved is not found: its URL
// comes from a view, which saves them.
func TestReadPartNotSaved(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)

	_, _, err := s.reader.Part(t.Context(), readUser, id, []int{1})
	require.ErrorIs(t, err, messagecontent.ErrPartNotFound)
	assert.Empty(t, s.provider.called())
}

// getContexts is a store that keeps the context of each Get.
type getContexts struct {
	messagecontent.Store
	got []context.Context
}

func (s *getContexts) Get(ctx context.Context, ref messagecontent.Ref) (io.ReadCloser, error) {
	s.got = append(s.got, ctx)
	return s.Store.Get(ctx, ref)
}

// A part's content is read without the request's deadline, which only
// bounds fetching it, so a slow download outlasts it.
func TestReadPartDeadline(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)
	_, err := s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	store := &getContexts{Store: s.store}
	reader := messagecontent.NewReader(store, readMemory{s.storage}, s.fetcher)

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	_, content, err := reader.Part(ctx, readUser, id, []int{1})
	require.NoError(t, err)
	require.NoError(t, content.Close())
	require.Len(t, store.got, 1)
	_, hasDeadline := store.got[0].Deadline()
	assert.False(t, hasDeadline)
	assert.Nil(t, store.got[0].Done(), "never cancelled")
}

// Without a provider, content not stored is not fetched.
func TestReadNoProvider(t *testing.T) {
	s, id := newReadStack(alternative("Hello"), nil)
	reader := messagecontent.NewReader(s.store, readMemory{s.storage}, nil)

	_, err := reader.View(t.Context(), readUser, id, partURL)
	require.ErrorIs(t, err, messagecontent.ErrNotStored)
	_, err = reader.Body(t.Context(), readUser, id, []int{1}, partURL)
	require.ErrorIs(t, err, messagecontent.ErrNotStored)
	_, err = s.reader.View(t.Context(), readUser, id, partURL)
	require.NoError(t, err)
	_, _, err = reader.Part(t.Context(), readUser, id, []int{1})
	require.NoError(t, err, "stored content reads")
}
