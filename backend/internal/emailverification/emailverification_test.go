package emailverification

import (
	"context"
	"crypto/sha256"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"amplop/internal/systememail"
)

const testUserID = int64(7)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// memoryStorage keeps one user's state and every user's links like the
// database does.
type memoryStorage struct {
	state      RequestState
	verifiedAt *time.Time
	links      []Link
	err        error
}

func (m *memoryStorage) RequestEmailVerification(_ context.Context, userID int64, create func(RequestState) (RequestChange, error)) error {
	if m.err != nil {
		return m.err
	}
	state := m.state
	state.LinksCreatedAt = nil
	for _, link := range m.links {
		if link.UserID == userID {
			state.LinksCreatedAt = append(state.LinksCreatedAt, link.CreatedAt)
		}
	}
	change, err := create(state)
	if err != nil {
		return err
	}
	kept := m.links[:0:0]
	for _, link := range m.links {
		if link.UserID != userID || link.ExpiresAt.After(change.ExpiredBy) {
			kept = append(kept, link)
		}
	}
	m.links = append(kept, change.Link)
	return nil
}

func (m *memoryStorage) RedeemEmailVerification(_ context.Context, hash [sha256.Size]byte, redeem func(RedeemState) (RedeemChange, error)) error {
	if m.err != nil {
		return m.err
	}
	i := slices.IndexFunc(m.links, func(link Link) bool { return link.TokenHash == hash })
	if i < 0 {
		return ErrLinkInvalid
	}
	link := m.links[i]
	change, err := redeem(RedeemState{Link: link, Email: m.state.Email, Verified: m.state.Verified})
	if err != nil {
		return err
	}
	m.links = slices.DeleteFunc(m.links, func(other Link) bool { return other.UserID == link.UserID })
	if change.VerifiedAt != nil {
		m.state.Verified, m.verifiedAt = true, change.VerifiedAt
	}
	return nil
}

// fakeMailer records each message and answers with err.
type fakeMailer struct {
	err  error
	sent []systememail.Outgoing
}

func (f *fakeMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	f.sent = append(f.sent, outgoing)
	return 1, f.err
}

func unverified() *memoryStorage {
	return &memoryStorage{state: RequestState{Email: "admin@example.org", MailConfigured: true}}
}

func testService(storage *memoryStorage, mailer *fakeMailer) *Service {
	publicURL := &url.URL{Scheme: "https", Host: "mail.example.com"}
	return NewService(storage, mailer, publicURL, func() time.Time { return testNow })
}

// sentLink returns the link at age before [testNow], for userID.
func sentLink(userID int64, age time.Duration) Link {
	createdAt := testNow.Add(-age)
	return Link{UserID: userID, Email: "admin@example.org", CreatedAt: createdAt, ExpiresAt: createdAt.Add(linkLifetime)}
}

// secretOf returns the secret in a verification email's link.
func secretOf(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, "https://mail.example.com/verify-email#")
	require.True(t, ok, "the body has a link")
	secret, _, _ := strings.Cut(after, "\n")
	return secret
}
