package registration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"amplop/internal/attemptlimit"
	"amplop/internal/emailaddress"
	"amplop/internal/systememail"
)

var testNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

const (
	visitor    = "visitor@example.org"
	otherEmail = "other@example.org"
)

var visitorAddr = netip.MustParseAddr("192.0.2.1")

// requestMemory keeps attempts and links like the database does.
type requestMemory struct {
	registration Record
	accounts     []string // emails that have an account
	attempts     []Attempt
	links        []Link
	since        time.Time
	err          error
	locked       int // calls to RequestRegistration
}

func (m *requestMemory) ReadRegistration(context.Context) (Record, error) {
	return m.registration, m.err
}

func (m *requestMemory) RequestRegistration(_ context.Context, email string, since time.Time, create func(RequestState) (RequestChange, error)) error {
	m.locked++
	if m.err != nil {
		return m.err
	}
	m.since = since
	kept := slices.DeleteFunc(slices.Clone(m.attempts), func(a Attempt) bool { return !a.CreatedAt.After(since) })
	change, err := create(RequestState{
		Registration:  m.registration,
		AccountExists: slices.Contains(m.accounts, email),
		Attempts:      kept,
	})
	if err != nil {
		return err
	}
	m.attempts = append(kept, change.Attempt)
	if change.Link != nil {
		m.links = append(m.links, *change.Link)
	}
	return nil
}

type fakeMailer struct {
	err  error
	sent []systememail.Outgoing
}

func (f *fakeMailer) Send(_ context.Context, outgoing systememail.Outgoing) (int64, error) {
	f.sent = append(f.sent, outgoing)
	return 1, f.err
}

// openStorage returns storage with registration open and no attempts.
func openStorage() *requestMemory {
	return &requestMemory{registration: Record{State: StateOpen, SystemEmailTested: true}}
}

func testRequests(storage *requestMemory, mailer *fakeMailer) *Requests {
	publicURL := &url.URL{Scheme: "https", Host: "mail.example.com"}
	return NewRequests(storage, mailer, publicURL, func() time.Time { return testNow }, slog.New(slog.DiscardHandler))
}

// attempts returns n attempts made age before [testNow].
func attempts(n int, address, client string, outcome Outcome, age time.Duration) []Attempt {
	made := make([]Attempt, n)
	for i := range made {
		made[i] = Attempt{Email: address, ClientPrefix: client, Outcome: outcome, CreatedAt: testNow.Add(-age)}
	}
	return made
}

// secretOf returns the secret in a registration link message's body.
func secretOf(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, "https://mail.example.com/register/complete#")
	require.True(t, ok, "the body has a link")
	secret, _, _ := strings.Cut(after, "\n")
	return secret
}

func TestRequestLink(t *testing.T) {
	storage, mailer := openStorage(), &fakeMailer{}

	err := testRequests(storage, mailer).Send(t.Context(), " Visitor@Example.org ", visitorAddr)
	require.NoError(t, err)

	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "registration link", sent.Purpose)
	assert.Equal(t, visitor, sent.To)
	assert.Equal(t, "Create your amplop account", sent.Subject)
	assert.True(t, sent.Private, "the body holds the secret")
	assert.Contains(t, sent.Body, "Someone asked to create an account on amplop at https://mail.example.com with this email address.")
	assert.Contains(t, sent.Body, "The link expires in 24 hours.")
	assert.Contains(t, sent.Body, "If you didn't ask for this, you can ignore this email.")

	// The link carries the secret; storage keeps only its hash.
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secretOf(t, sent.Body))
	require.NoError(t, err)
	assert.Equal(t, []Link{{
		TokenHash: sha256.Sum256(raw), Email: visitor, CreatedAt: testNow, ExpiresAt: testNow.Add(24 * time.Hour),
	}}, storage.links)
	assert.Equal(t, []Attempt{{Email: visitor, ClientPrefix: "192.0.2.1/32", Outcome: OutcomeLink, CreatedAt: testNow}}, storage.attempts)

	// Attempts are kept for the longest window.
	assert.Equal(t, testNow.Add(-24*time.Hour), storage.since)
}

// An email with an account gets a notice instead of a link, so its owner
// learns of the request and the visitor learns nothing.
func TestRequestNotice(t *testing.T) {
	storage, mailer := openStorage(), &fakeMailer{}
	storage.accounts = []string{visitor}

	err := testRequests(storage, mailer).Send(t.Context(), visitor, visitorAddr)
	require.NoError(t, err)

	require.Len(t, mailer.sent, 1)
	sent := mailer.sent[0]
	assert.Equal(t, "registration notice", sent.Purpose)
	assert.Equal(t, visitor, sent.To)
	assert.Equal(t, "Your email address already has an amplop account", sent.Subject)
	assert.False(t, sent.Private, "the body holds no secret")
	assert.Contains(t, sent.Body, "You already have an account there, so no new one was created.")
	assert.Contains(t, sent.Body, "https://mail.example.com/sign-in\n")
	assert.NotContains(t, sent.Body, "register/complete")
	assert.Empty(t, storage.links)
	assert.Equal(t, []Attempt{{Email: visitor, ClientPrefix: "192.0.2.1/32", Outcome: OutcomeNotice, CreatedAt: testNow}}, storage.attempts)
}

// An email that reached its limit gets nothing, and the request still
// succeeds, so the answer does not reveal earlier requests for it. Links and
// notices count; skipped requests do not, or an attacker could keep an
// email locked out for as long as they kept asking.
func TestRequestEmailLimit(t *testing.T) {
	for _, c := range []struct {
		name     string
		earlier  []Attempt
		accounts []string
		sends    bool
	}{
		{"3 links in an hour", attempts(3, visitor, "198.51.100.1/32", OutcomeLink, 59*time.Minute), nil, false},
		{"3 notices in an hour", attempts(3, visitor, "198.51.100.1/32", OutcomeNotice, 59*time.Minute), []string{visitor}, false},
		{"5 in a day", attempts(5, visitor, "198.51.100.1/32", OutcomeLink, 23*time.Hour), nil, false},
		{"2 in the hour", attempts(2, visitor, "198.51.100.1/32", OutcomeLink, 59*time.Minute), nil, true},
		{"3 an hour ago", attempts(3, visitor, "198.51.100.1/32", OutcomeLink, time.Hour), nil, true},
		{"skipped ones", attempts(4, visitor, "198.51.100.1/32", OutcomeSkipped, time.Minute), nil, true},
		// Other clients' requests, under their own limit, count only toward everyone's.
		{"other emails", attempts(10, otherEmail, "198.51.100.1/32", OutcomeLink, time.Minute), nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := openStorage(), &fakeMailer{}
			storage.attempts, storage.accounts = c.earlier, c.accounts

			err := testRequests(storage, mailer).Send(t.Context(), visitor, visitorAddr)
			require.NoError(t, err)

			assert.Equal(t, c.sends, len(mailer.sent) == 1)
			recorded := storage.attempts[len(storage.attempts)-1]
			assert.Equal(t, c.sends, recorded.Outcome != OutcomeSkipped, "a skipped request is still recorded")
			if !c.sends {
				assert.Empty(t, storage.links)
			}
		})
	}
}

// Too many requests from one client address, or from everyone, are refused
// with the wait. Every recorded request counts, skipped ones included. A
// refusal by the limit on everyone logs a warning, so an administrator can
// see someone keeping registration full; one client's refusal does not.
func TestRequestThrottled(t *testing.T) {
	for _, c := range []struct {
		name    string
		earlier []Attempt
		wait    time.Duration
		warned  bool
	}{
		{"10 from the client in 15 minutes", attempts(10, otherEmail, "192.0.2.1/32", OutcomeSkipped, 5*time.Minute), 10 * time.Minute, false},
		{"50 from everyone in an hour", attempts(50, otherEmail, "198.51.100.1/32", OutcomeSkipped, 20*time.Minute), 40 * time.Minute, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := openStorage(), &fakeMailer{}
			storage.attempts = c.earlier
			var logs bytes.Buffer
			publicURL := &url.URL{Scheme: "https", Host: "mail.example.com"}
			requests := NewRequests(storage, mailer, publicURL, func() time.Time { return testNow }, slog.New(slog.NewTextHandler(&logs, nil)))

			err := requests.Send(t.Context(), visitor, visitorAddr)

			limited, ok := errors.AsType[*attemptlimit.Error](err)
			require.True(t, ok, "%v", err)
			require.ErrorIs(t, err, ErrThrottled)
			assert.Equal(t, c.wait, limited.Wait)
			assert.Empty(t, mailer.sent)
			assert.Equal(t, c.earlier, storage.attempts, "a refused request is not recorded")
			if c.warned {
				assert.Contains(t, logs.String(), `level=WARN msg="registration limit on everyone reached" wait=40m0s`)
			} else {
				assert.Empty(t, logs.String())
			}
			assert.NotContains(t, logs.String(), visitor, "the log names no email")
		})
	}
}

// The limits on the client and on everyone come before the email's own, so
// a 429 never depends on which email was asked for.
func TestRequestThrottledBeforeEmailLimit(t *testing.T) {
	storage, mailer := openStorage(), &fakeMailer{}
	storage.attempts = append(attempts(3, visitor, "198.51.100.1/32", OutcomeLink, time.Minute),
		attempts(10, otherEmail, "192.0.2.1/32", OutcomeLink, time.Minute)...)

	err := testRequests(storage, mailer).Send(t.Context(), visitor, visitorAddr)

	require.ErrorIs(t, err, ErrThrottled)
}

// Closed or paused registration refuses before anything is counted or sent.
func TestRequestClosed(t *testing.T) {
	for _, c := range []struct {
		name   string
		record Record
	}{
		{"closed", Record{State: StateClosed, SystemEmailTested: true}},
		{"paused", Record{State: StateOpen}},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := openStorage(), &fakeMailer{}
			storage.registration = c.record
			// Closed answers first, even for a throttled client.
			storage.attempts = attempts(10, otherEmail, "192.0.2.1/32", OutcomeLink, time.Minute)

			err := testRequests(storage, mailer).Send(t.Context(), visitor, visitorAddr)

			require.ErrorIs(t, err, ErrClosed)
			assert.Zero(t, storage.locked, "refused before the lock every request shares")
			assert.Empty(t, mailer.sent)
			assert.Len(t, storage.attempts, 10)
		})
	}
}

func TestRequestInvalid(t *testing.T) {
	for _, c := range []struct {
		name   string
		email  string
		client netip.Addr
		want   error
	}{
		{"email", "Visitor <visitor@example.org>", visitorAddr, emailaddress.ErrInvalid},
		{"client", visitor, netip.Addr{}, errClientAddr},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := openStorage(), &fakeMailer{}

			err := testRequests(storage, mailer).Send(t.Context(), c.email, c.client)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, storage.attempts)
			assert.Empty(t, mailer.sent)
		})
	}
}

// The client limit counts a client as [clientprefix.Of] does: an IPv4 address
// mapped into IPv6 as the IPv4 address, and every address in an IPv6 /64 as
// one client, so a host cannot dodge it by changing address.
func TestRequestClientPrefix(t *testing.T) {
	for _, c := range []struct {
		name, saved, from string
		throttled         bool
	}{
		{"mapped IPv4", "192.0.2.1/32", "::ffff:192.0.2.1", true},
		{"same /64", "2001:db8:1:2::/64", "2001:db8:1:2:ffff::9", true},
		{"other /64", "2001:db8:1:2::/64", "2001:db8:1:3::1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := openStorage()
			storage.attempts = attempts(10, otherEmail, c.saved, OutcomeLink, time.Minute)

			err := testRequests(storage, &fakeMailer{}).Send(t.Context(), visitor, netip.MustParseAddr(c.from))

			if c.throttled {
				require.ErrorIs(t, err, ErrThrottled)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "2001:db8:1:3::/64", storage.attempts[len(storage.attempts)-1].ClientPrefix)
		})
	}
}

// A failed send is returned. The attempt still counts, and the link stays:
// a send that timed out may still arrive, and an undelivered link is useless
// to anyone else until cleanup deletes it.
func TestRequestSendFailure(t *testing.T) {
	failed := &systememail.SendError{Reason: systememail.ReasonTimeout, Err: errors.New("timeout")}
	for _, c := range []struct {
		name     string
		accounts []string
		links    int
	}{
		{"link", nil, 1},
		{"notice", []string{visitor}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage, mailer := openStorage(), &fakeMailer{err: failed}
			storage.accounts = c.accounts

			err := testRequests(storage, mailer).Send(t.Context(), visitor, visitorAddr)

			require.ErrorIs(t, err, failed)
			assert.Len(t, storage.attempts, 1)
			assert.Len(t, storage.links, c.links)
		})
	}
}

func TestRequestStorageError(t *testing.T) {
	lost := errors.New("connection lost")
	storage, mailer := &requestMemory{err: lost}, &fakeMailer{}

	err := testRequests(storage, mailer).Send(t.Context(), visitor, visitorAddr)

	require.ErrorIs(t, err, lost)
	assert.Empty(t, mailer.sent)
}

// Attempts are kept as long as any limit counts them.
func TestRetentionCoversLimits(t *testing.T) {
	for _, limit := range []attemptlimit.Limit{emailShortLimit, emailLongLimit, clientLimit, instanceLimit} {
		assert.GreaterOrEqual(t, attemptRetention, limit.Window)
	}
}

// Registration that closes while a request waits for the lock is still
// refused under it.
func TestRequestClosedWhileWaiting(t *testing.T) {
	storage, mailer := &closingStorage{requestMemory: openStorage()}, &fakeMailer{}

	err := NewRequests(storage, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"}, func() time.Time { return testNow }, slog.New(slog.DiscardHandler)).Send(t.Context(), visitor, visitorAddr)

	require.ErrorIs(t, err, ErrClosed)
	assert.Empty(t, mailer.sent)
	assert.Empty(t, storage.attempts)
}

// closingStorage reads registration as open, then closes it before the lock.
type closingStorage struct{ *requestMemory }

func (c *closingStorage) RequestRegistration(ctx context.Context, email string, since time.Time, create func(RequestState) (RequestChange, error)) error {
	c.registration = Record{State: StateClosed, SystemEmailTested: true}
	return c.requestMemory.RequestRegistration(ctx, email, since, create)
}
