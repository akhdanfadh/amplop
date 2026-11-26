package messagecontent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"uuid"
)

// TextLimits bounds what [Texts.Read] reads of one message.
type TextLimits struct {
	Parts int // the most text parts it opens, one the store lost included
	Bytes int // the most bytes it reads, over all of them
}

// StoredText is the stored text of one text part of a message.
type StoredText struct {
	MediaType string // "text/plain" or "text/html"
	Content   []byte // as stored: UTF-8
}

// MessageTexts is the stored text of a message's text parts, by whether a
// view shows them, and whether a view offers a download.
type MessageTexts struct {
	// Shown is the text of the bodies a view shows, as [Choose] picks
	// them, in that order.
	Shown []StoredText
	// Hidden is the text of every other text part, in the message's order,
	// depth first: the alternatives a view does not pick, and a text part
	// a multipart/related shows only as a download.
	Hidden []StoredText
	// HasAttachment is whether a view offers any of the message's parts as
	// a download, as [Choose] picks them, e.g. a PDF or a photo, but not a
	// logo its HTML shows. An inline part that the HTML never refers to,
	// which [Display.Downloads] also offers, does not count: telling it
	// apart takes the sanitized HTML.
	HasAttachment bool
}

// TextStorage finds the parts saved for messages.
type TextStorage interface {
	// MessageParts returns the part tree of account accountID's message
	// messageID, and false when it has none, or no such message.
	MessageParts(ctx context.Context, accountID int64, messageID uuid.UUID) (Part, bool, error)
}

// Texts reads the stored text of messages, e.g. for the search index. It
// never fetches from the provider.
type Texts struct {
	store   Store
	storage TextStorage
}

// NewTexts returns a reader of text kept in store and storage.
func NewTexts(store Store, storage TextStorage) *Texts {
	return &Texts{store: store, storage: storage}
}

// Read returns the stored text of every text part of account accountID's
// message messageID, shown and hidden apart, within limits shared by both:
// the shown parts are read first, so the hidden ones get what they leave,
// and the last part read may be cut short. It also returns whether a view
// offers a download, from the whole part tree, whatever the limits leave.
// It returns none when the message has no parts stored, and leaves out a
// part whose content the store lacks.
//
// DESIGN: a part's [Part.BlobID] tells shown from hidden: every text part
// with stored content has its own, and one without has no text to read.
func (t *Texts) Read(ctx context.Context, accountID int64, messageID uuid.UUID, limits TextLimits) (MessageTexts, error) {
	root, ok, err := t.storage.MessageParts(ctx, accountID, messageID)
	if err != nil || !ok {
		return MessageTexts{}, err
	}
	display := Choose(root)
	shown := display.Bodies
	inView := make(map[uuid.UUID]bool, len(shown))
	for _, part := range shown {
		inView[part.BlobID] = true
	}
	var hidden []Part
	for _, part := range leaves(root) {
		if part.Kind == KindText && !inView[part.BlobID] {
			hidden = append(hidden, part)
		}
	}

	r := reader{texts: t, account: accountID, limits: limits}
	texts := MessageTexts{HasAttachment: len(display.Attachments) > 0}
	if texts.Shown, err = r.read(ctx, shown); err != nil {
		return MessageTexts{}, err
	}
	if texts.Hidden, err = r.read(ctx, hidden); err != nil {
		return MessageTexts{}, err
	}
	return texts, nil
}

// reader reads parts of one account's message within limits, which it
// spends as it reads.
type reader struct {
	texts   *Texts
	account int64
	limits  TextLimits
}

// read returns the stored text of parts, in order, until the limits run
// out, leaving out a part whose content the store lacks.
func (r *reader) read(ctx context.Context, parts []Part) ([]StoredText, error) {
	var out []StoredText
	for _, part := range parts {
		// A sender can send hundreds of empty parts, each one more request
		// to the store, which the byte budget alone would not stop.
		if r.limits.Parts <= 0 || r.limits.Bytes <= 0 {
			break
		}
		r.limits.Parts--
		content, err := r.texts.get(ctx, r.account, part, r.limits.Bytes)
		// Content a row names but the store lost, e.g. with a volume: the
		// message is still found by the rest.
		if errors.Is(err, ErrNotStored) {
			continue
		}
		if err != nil {
			return nil, err
		}
		r.limits.Bytes -= len(content)
		out = append(out, StoredText{MediaType: part.MediaType, Content: content})
	}
	return out, nil
}

// get returns at most limit bytes of the stored content of part, of
// account accountID's message, or [ErrNotStored] when it has none.
func (t *Texts) get(ctx context.Context, accountID int64, part Part, limit int) ([]byte, error) {
	if part.BlobID == uuid.Nil() {
		return nil, fmt.Errorf("part %q: %w", part.ID, ErrNotStored)
	}
	stream, err := t.store.Get(ctx, Ref{Account: accountID, ID: part.BlobID})
	if err != nil {
		return nil, fmt.Errorf("get part %q: %w", part.ID, err)
	}
	defer stream.Close()
	content, err := io.ReadAll(io.LimitReader(stream, int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("read part %q: %w", part.ID, err)
	}
	return content, nil
}
