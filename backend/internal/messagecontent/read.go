package messagecontent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"
	"uuid"
)

// textClip is how much of a plain-text body a view gets, inbok's choice
// after Gmail, which clips a message past about 102 KB: a view stays quick
// to load and show, and the whole text remains a download.
const textClip = 102 << 10

// ReadStorage finds members' messages and the parts saved for them.
type ReadStorage interface {
	// UserMessageAccount returns the connected account of user userID's
	// message messageID, or [ErrMessageNotFound] when none of the user's
	// accounts has it.
	UserMessageAccount(ctx context.Context, userID int64, messageID uuid.UUID) (int64, error)
	// MessageParts returns the part tree of account accountID's message
	// messageID, and false when it has none, or no such message.
	MessageParts(ctx context.Context, accountID int64, messageID uuid.UUID) (Part, bool, error)
}

// fetches fetches from the provider content a view needs that is not
// stored, e.g. [Fetcher].
type fetches interface {
	FetchMessage(ctx context.Context, accountID int64, messageID uuid.UUID) error
	FetchPart(ctx context.Context, accountID int64, messageID uuid.UUID, path []int) (Part, error)
}

// PartURL returns the URL that serves the part at path of the message a
// view shows.
type PartURL func(path []int) string

// View is what a member's view of a message shows: its bodies, one after
// another, and the parts it offers as downloads, as [Choose] picks them.
type View struct {
	Bodies    []Body
	Downloads []Download
	// Blocked says whether an HTML body refers to remote images that
	// [RemoteBlocked] leaves out, so the view can offer to load them.
	Blocked bool
}

// Body is one body a view shows.
type Body struct {
	Part Part
	Path []int // the part's place, as [Fetcher.FetchPart] takes it
	// Text is a plain-text body's text, clipped past about 100 KB; nil
	// for HTML, which the view loads by URL.
	Text    []byte
	Clipped bool // whether Text is only the start of the text
}

// Download is one part a view offers as a download.
type Download struct {
	Part Part
	Path []int // the part's place, as [Fetcher.FetchPart] takes it
}

// HTMLBody is an HTML body's stored content, with the policy that
// sanitizes it for its message's view but for [Policy.Remote].
type HTMLBody struct {
	Content io.ReadCloser
	Policy  Policy
}

// Reader reads members' messages' content for their views, fetching from
// the provider what is not stored.
type Reader struct {
	store   Store
	storage ReadStorage
	fetches fetches
}

// NewReader returns a reader of content kept in store and storage, which
// fetches what is missing with fetches; with fetches nil, as when no
// provider is configured, content not stored is [ErrNotStored].
func NewReader(store Store, storage ReadStorage, fetches fetches) *Reader {
	return &Reader{store: store, storage: storage, fetches: fetches}
}

// View returns what the view of user userID's message messageID shows,
// with partURL building its inline images' URLs. A message whose content
// is not stored, or only partly, is fetched first, as [Fetcher.FetchMessage]
// does. It returns [ErrMessageNotFound] when none of the user's accounts
// has the message, an error of [Fetcher.FetchMessage], or a store's or
// storage's error.
func (r *Reader) View(ctx context.Context, userID int64, messageID uuid.UUID, partURL PartURL) (View, error) {
	accountID, err := r.storage.UserMessageAccount(ctx, userID, messageID)
	if err != nil {
		return View{}, err
	}
	view, err := r.view(ctx, accountID, messageID, partURL)
	if !errors.Is(err, ErrNotStored) || r.fetches == nil {
		return view, err
	}
	// Content the background fetch has not reached yet, e.g. older than
	// its window, or that the store lost: fetching it again restores it.
	if err := r.fetches.FetchMessage(ctx, accountID, messageID); err != nil {
		return View{}, err
	}
	return r.view(ctx, accountID, messageID, partURL)
}

// view returns what the view of account accountID's message messageID
// shows, from what is stored. It returns [ErrNotStored] when the message
// has no parts saved, or a body's content is not stored.
func (r *Reader) view(ctx context.Context, accountID int64, messageID uuid.UUID, partURL PartURL) (View, error) {
	root, ok, err := r.storage.MessageParts(ctx, accountID, messageID)
	if err != nil {
		return View{}, fmt.Errorf("read message parts: %w", err)
	}
	if !ok {
		return View{}, fmt.Errorf("message parts: %w", ErrNotStored)
	}
	paths := partPaths(root)
	display := Choose(root)
	policy := Policy{Inline: display.Inline, PartURL: partURL.byID(paths), Remote: RemoteBlocked}

	var view View
	var shown []string
	for _, part := range display.Bodies {
		body := Body{Part: part, Path: paths[part.ID]}
		content, err := r.open(ctx, accountID, part)
		if err != nil {
			return View{}, err
		}
		if isHTML(part) {
			// The frame loads the body itself; this only learns what it
			// shows and leaves out.
			found, err := Sanitize(io.Discard, content, policy)
			content.Close()
			if err != nil {
				return View{}, fmt.Errorf("read part %q: %w", part.ID, err)
			}
			shown = append(shown, found.Shown...)
			view.Blocked = view.Blocked || found.Blocked
		} else {
			// One byte past the clip says whether there is more.
			text, err := io.ReadAll(io.LimitReader(content, textClip+1))
			content.Close()
			if err != nil {
				return View{}, fmt.Errorf("read part %q: %w", part.ID, err)
			}
			body.Text, body.Clipped = clip(text)
		}
		view.Bodies = append(view.Bodies, body)
	}
	for _, part := range display.Downloads(shown) {
		view.Downloads = append(view.Downloads, Download{Part: part, Path: paths[part.ID]})
	}
	return view, nil
}

// Body returns the stored content of the HTML body at path of user
// userID's message messageID, with the policy that sanitizes it, whose
// inline images' URLs partURL builds. A body not stored is fetched first,
// as [Fetcher.FetchMessage] does. It returns [ErrMessageNotFound] when
// none of the user's accounts has the message, [ErrPartNotFound] when no
// HTML body its view shows is at path, an error of [Fetcher.FetchMessage],
// or a store's or storage's error. The caller closes the content.
func (r *Reader) Body(ctx context.Context, userID int64, messageID uuid.UUID, path []int, partURL PartURL) (HTMLBody, error) {
	accountID, err := r.storage.UserMessageAccount(ctx, userID, messageID)
	if err != nil {
		return HTMLBody{}, err
	}
	body, err := r.body(ctx, accountID, messageID, path, partURL)
	if !errors.Is(err, ErrNotStored) || r.fetches == nil {
		return body, err
	}
	// As in [Reader.View].
	if err := r.fetches.FetchMessage(ctx, accountID, messageID); err != nil {
		return HTMLBody{}, err
	}
	return r.body(ctx, accountID, messageID, path, partURL)
}

// body returns the stored HTML body at path of account accountID's
// message messageID. It returns [ErrNotStored] when the message has no
// parts saved, or the body's content is not stored.
func (r *Reader) body(ctx context.Context, accountID int64, messageID uuid.UUID, path []int, partURL PartURL) (HTMLBody, error) {
	root, ok, err := r.storage.MessageParts(ctx, accountID, messageID)
	if err != nil {
		return HTMLBody{}, fmt.Errorf("read message parts: %w", err)
	}
	if !ok {
		return HTMLBody{}, fmt.Errorf("message parts: %w", ErrNotStored)
	}
	part, found := partAt(root, path)
	display := Choose(root)
	// Only a body the view shows is served as a document: any other part,
	// e.g. HTML sent as an attachment, is a download.
	if !found || !isHTML(part) || !inView(root, display.Bodies, part, path) {
		return HTMLBody{}, ErrPartNotFound
	}
	content, err := r.open(ctx, accountID, part)
	if err != nil {
		return HTMLBody{}, err
	}
	policy := Policy{Inline: display.Inline, PartURL: partURL.byID(partPaths(root))}
	return HTMLBody{Content: content, Policy: policy}, nil
}

// Part returns the part at path of user userID's message messageID, and
// its stored content: a text part's in UTF-8, any other's as received. A
// part not stored yet, e.g. an attachment on its first download, is
// fetched first, as [Fetcher.FetchPart] does. It returns
// [ErrMessageNotFound] when none of the user's accounts has the message,
// [ErrPartNotFound] when the message's view neither shows nor offers a
// part at path, an error of [Fetcher.FetchPart], or a store's or
// storage's error. The caller closes the content.
//
// DESIGN: The content is read without ctx's deadline, so a large download
// to a slow client outlasts the time a request has for its work; the
// store bounds a read, and closing the content stops it. Fetching keeps
// ctx's deadline.
func (r *Reader) Part(ctx context.Context, userID int64, messageID uuid.UUID, path []int) (Part, io.ReadCloser, error) {
	accountID, err := r.storage.UserMessageAccount(ctx, userID, messageID)
	if err != nil {
		return Part{}, nil, err
	}
	root, ok, err := r.storage.MessageParts(ctx, accountID, messageID)
	if err != nil {
		return Part{}, nil, fmt.Errorf("read message parts: %w", err)
	}
	part, found := partAt(root, path)
	// A part URL comes from a view, which saved the parts; only the parts
	// it shows or offers are served.
	display := Choose(root)
	if !ok || !found || !inView(root, slices.Concat(display.Bodies, display.Attachments, display.Inline), part, path) {
		return Part{}, nil, ErrPartNotFound
	}

	read := context.WithoutCancel(ctx)
	if part.BlobID != uuid.Nil() {
		content, err := r.open(read, accountID, part)
		if err == nil {
			return part, content, nil
		}
		if !errors.Is(err, ErrNotStored) {
			return Part{}, nil, err
		}
	}
	if r.fetches == nil {
		return Part{}, nil, fmt.Errorf("part %q: %w", part.ID, ErrNotStored)
	}
	// Not stored yet, or the store lost it: fetching stores it again.
	part, err = r.fetches.FetchPart(ctx, accountID, messageID, path)
	if err != nil {
		return Part{}, nil, err
	}
	content, err := r.open(read, accountID, part)
	if err != nil {
		return Part{}, nil, err
	}
	return part, content, nil
}

// open returns the stored content of part, of account accountID's
// message, or [ErrNotStored] when it has none.
func (r *Reader) open(ctx context.Context, accountID int64, part Part) (io.ReadCloser, error) {
	if part.BlobID == uuid.Nil() {
		return nil, fmt.Errorf("part %q: %w", part.ID, ErrNotStored)
	}
	content, err := r.store.Get(ctx, Ref{Account: accountID, ID: part.BlobID})
	if err != nil {
		return nil, fmt.Errorf("get part %q: %w", part.ID, err)
	}
	return content, nil
}

// byID returns a [Policy.PartURL] that builds, with u, the URL of the part
// with an ID in paths.
func (u PartURL) byID(paths map[string][]int) func(partID string) string {
	return func(partID string) string {
		return u(paths[partID])
	}
}

// partPaths returns the path of each part under root, root included, by
// part ID. Of parts that share an ID, the first in message order keeps it.
func partPaths(root Part) map[string][]int {
	paths := map[string][]int{}
	var walk func(part Part, path []int)
	walk = func(part Part, path []int) {
		if _, ok := paths[part.ID]; !ok {
			paths[part.ID] = path
		}
		for i, child := range part.Parts {
			// A copy, so siblings' paths share no array.
			walk(child, append(slices.Clone(path), i))
		}
	}
	walk(root, []int{})
	return paths
}

// inView reports whether part, at path of the message whose part tree is
// root, is one of shown, by ID, and the part with its ID that a view
// names: its first in message order.
func inView(root Part, shown []Part, part Part, path []int) bool {
	return slices.Equal(partPaths(root)[part.ID], path) &&
		slices.ContainsFunc(shown, func(other Part) bool { return other.ID == part.ID })
}

// clip returns text cut at a character boundary to at most [textClip]
// bytes, and whether it cut.
func clip(text []byte) ([]byte, bool) {
	if len(text) <= textClip {
		return text, false
	}
	end := textClip
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}
