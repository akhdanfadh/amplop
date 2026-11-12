package messagecontent

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"inbok/internal/connectedaccount"
)

// Errors a [Provider] returns, which end a fetch for good: fetching again
// soon gives the same answer.
var (
	// ErrGone means the provider no longer has the message, or the message
	// no such part.
	ErrGone = errors.New("messagecontent: the provider no longer has it")
	// ErrTooLarge means the provider's answer passed inbok's cap.
	ErrTooLarge = errors.New("messagecontent: the provider's answer is too large")
	// ErrUnreadable means the provider refused the request for this
	// message, or its answer could not be read, e.g. content that is not
	// the encoding it should be.
	ErrUnreadable = errors.New("messagecontent: the provider refused the message or its answer cannot be read")
)

// ErrPartNotFound means a message has no saved part at a path, or only a
// multipart there, which has no content of its own.
var ErrPartNotFound = errors.New("messagecontent: message part not found")

// Provider fetches a message's content from its provider, with an access
// token for the account. Each method only reads, so a retried call
// changes nothing. Each returns [ErrGone] when the provider no longer has
// the message, [ErrTooLarge] when its answer passes inbok's cap, and
// [ErrUnreadable] when it refuses the request for this message or its
// answer cannot be read. Any other error, e.g. the provider's rate limits
// or a network failure, may pass by itself.
type Provider interface {
	// Message returns account accountID's message providerID whole, as
	// [Parse] reads it, keeping the account's fetches, across calls, to
	// the provider's limits for one account.
	Message(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerID string) (Received, error)
	// PartContent returns the content of message providerID's part
	// partID, a [Part.ID], transfer encoding undone, and the charset the
	// provider converted it to, as [Received.ContentCharset], keeping the
	// account's fetches to its limits as [Provider.Message] does. It
	// returns [ErrGone] also when the message has no such part.
	PartContent(ctx context.Context, token connectedaccount.AccessToken, accountID int64, providerID, partID string) ([]byte, string, error)
}

// accountAccess hands out access tokens, e.g. [connectedaccount.Access].
type accountAccess interface {
	Token(ctx context.Context, accountID int64) (connectedaccount.AccessToken, error)
}

// FetchStorage keeps what a [Fetcher] reads about messages.
type FetchStorage interface {
	// MessageProviderID returns the provider's ID of account accountID's
	// message messageID, or [ErrMessageNotFound] when there is no such
	// message.
	MessageProviderID(ctx context.Context, accountID int64, messageID uuid.UUID) (string, error)
	// MessageParts returns the part tree of account accountID's message
	// messageID, and false when it has none, or no such message.
	MessageParts(ctx context.Context, accountID int64, messageID uuid.UUID) (Part, bool, error)
}

// Fetcher fetches messages' content from their provider and keeps it in
// [Contents]: a message or part a member opens.
type Fetcher struct {
	contents *Contents
	storage  FetchStorage
	provider Provider
	access   accountAccess
}

// NewFetcher returns a [Fetcher] keeping content in contents, given
// storage, the provider it fetches from, and access to its accounts'
// tokens.
func NewFetcher(contents *Contents, storage FetchStorage, provider Provider, access accountAccess) *Fetcher {
	return &Fetcher{contents: contents, storage: storage, provider: provider, access: access}
}

// FetchMessage fetches account accountID's message messageID's content
// from its provider and saves it, as when a member opens it. A message
// whose parts are saved keeps them, and its text is stored again under
// the IDs they name, which restores text the store lost. It returns [ErrMessageNotFound], an
// access error from [connectedaccount.Access.Token], a [Provider]'s
// errors, [ErrTooManyParts], or a store's or storage's error.
func (f *Fetcher) FetchMessage(ctx context.Context, accountID int64, messageID uuid.UUID) error {
	providerID, err := f.storage.MessageProviderID(ctx, accountID, messageID)
	if err != nil {
		return err
	}
	token, err := f.access.Token(ctx, accountID)
	if err != nil {
		return err
	}
	return f.fetch(ctx, token, accountID, messageID, providerID)
}

// fetch fetches account accountID's message messageID, providerID at its
// provider, with token, and saves its parts and text.
func (f *Fetcher) fetch(ctx context.Context, token connectedaccount.AccessToken, accountID int64, messageID uuid.UUID, providerID string) error {
	received, err := f.provider.Message(ctx, token, accountID, providerID)
	if err != nil {
		return fmt.Errorf("fetch message: %w", err)
	}
	root, texts, err := Parse(received)
	if err != nil {
		return err
	}
	// A provider may send a long text part separately, e.g. Gmail by
	// attachmentId; without it, the message would show no text.
	for _, part := range separateTexts(root, texts) {
		content, contentCharset, err := f.provider.PartContent(ctx, token, accountID, providerID, part.ID)
		if err != nil {
			return fmt.Errorf("fetch part %q: %w", part.ID, err)
		}
		texts = append(texts, Text{PartID: part.ID, Content: DecodeFrom(part, contentCharset, content)})
	}
	return f.contents.Save(ctx, accountID, messageID, root, texts)
}

// separateTexts returns the text parts of root that a view may show, as
// [Parse] collects them, that have no [Text] in texts: those whose
// content the provider sends separately.
func separateTexts(root Part, texts []Text) []Part {
	have := make(map[string]bool, len(texts))
	for _, text := range texts {
		have[text.PartID] = true
	}
	var missing []Part
	var walk func(part Part)
	walk = func(part Part) {
		if part.Kind == KindText && !have[part.ID] {
			missing = append(missing, part)
		}
		// Only a multipart's children are shown, as in parse.
		if part.Kind != KindMultipart {
			return
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(root)
	return missing
}

// FetchPart fetches the content of the part at path of account
// accountID's message messageID from its provider, stores it, and returns
// the part naming it: a text part's content converted to UTF-8 as
// [DecodeFrom] does, any other's as the provider gives it. A part stored before is
// stored again under its ID, which restores content the store lost. The
// message's parts must be saved, e.g. by [Fetcher.FetchMessage]. It
// returns [ErrMessageNotFound], [ErrPartNotFound] when the message has no
// saved part there or only a multipart, an access error, a [Provider]'s
// errors, or a store's or storage's error.
func (f *Fetcher) FetchPart(ctx context.Context, accountID int64, messageID uuid.UUID, path []int) (Part, error) {
	providerID, err := f.storage.MessageProviderID(ctx, accountID, messageID)
	if err != nil {
		return Part{}, err
	}
	root, ok, err := f.storage.MessageParts(ctx, accountID, messageID)
	if err != nil {
		return Part{}, fmt.Errorf("read message parts: %w", err)
	}
	part, found := partAt(root, path)
	if !ok || !found || part.Kind == KindMultipart {
		return Part{}, ErrPartNotFound
	}

	token, err := f.access.Token(ctx, accountID)
	if err != nil {
		return Part{}, err
	}
	content, contentCharset, err := f.provider.PartContent(ctx, token, accountID, providerID, part.ID)
	if err != nil {
		return Part{}, fmt.Errorf("fetch part: %w", err)
	}
	// A file is kept as received; a provider may have converted a text
	// part's bytes, e.g. Gmail's inline data, which then stays converted.
	if part.Kind == KindText {
		content = DecodeFrom(part, contentCharset, content)
	}
	return f.contents.SavePart(ctx, accountID, messageID, path, part, content)
}

// partAt returns the part of root at path, the index of each part on the
// way down, and whether there is one.
func partAt(root Part, path []int) (Part, bool) {
	part := root
	for _, i := range path {
		if i < 0 || i >= len(part.Parts) {
			return Part{}, false
		}
		part = part.Parts[i]
	}
	return part, true
}
