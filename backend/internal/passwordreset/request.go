package passwordreset

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"inbok/internal/attemptlimit"
	"inbok/internal/clientprefix"
	"inbok/internal/emailaddress"
)

// Product policy, the same as registration's email and client limits. The
// email limits keep one inbox from being flooded; the client limit slows one
// visitor.
var (
	emailShortLimit = attemptlimit.Limit{Window: time.Hour, Max: 3}
	emailLongLimit  = attemptlimit.Limit{Window: 24 * time.Hour, Max: 5}
	clientLimit     = attemptlimit.Limit{Window: 15 * time.Minute, Max: 10}
)

// attemptRetention is the longest window, so dropping older attempts loses
// no count.
const attemptRetention = 24 * time.Hour

var (
	// ErrThrottled means the client has asked for too many reset links
	// recently.
	ErrThrottled = errors.New("password reset requests throttled")

	errClientAddr = errors.New("invalid client address")
)

// requestStorage records reset requests.
type requestStorage interface {
	// RecordPasswordResetRequest serializes the requests of client, an
	// IPv4 address or an IPv6 /64 written as a prefix, and calls check with
	// the times of client's requests after since. If check succeeds, it
	// records an attempt from client at the time check returns, looks up
	// the ID of the account with email, if any, and saves the job that
	// sends that account a link, all in one transaction and the same work
	// for every email. An error from check rolls back and is returned
	// unchanged.
	RecordPasswordResetRequest(ctx context.Context, client, email string, since time.Time, check func([]time.Time) (time.Time, error)) error
}

// Requests answers requests for reset links.
type Requests struct {
	storage requestStorage
	now     func() time.Time
}

// NewRequests returns [Requests] given storage and a clock.
func NewRequests(storage requestStorage, now func() time.Time) *Requests {
	return &Requests{storage: storage, now: now}
}

// Request accepts a request from client for a reset link to email and
// returns nil, the same for every address. Once it records the attempt, a
// background job, which it does not wait for, sends a link if email belongs
// to an active account with a verified email under its limits, and nothing
// otherwise. It returns [emailaddress.ErrInvalid] for an invalid email, and
// an [*attemptlimit.Error] wrapping [ErrThrottled] for a client over its
// limit, both without recording anything, and storage's error if recording
// fails.
//
// DESIGN: before it returns, Request does the same work for every address:
// it records the attempt, looks up the account's ID, and saves the job,
// whether or not an account has the email. Everything that differs, such
// as checking the account and sending, runs in the job, so the answer and
// its timing are nearly the same for every address, the lookup costing a
// few microseconds more when it finds a row; OWASP's Forgot Password Cheat
// Sheet asks for that. The email limits are silent for the same reason:
// past them, nothing is sent and the answer does not change.
func (r *Requests) Request(ctx context.Context, email string, client netip.Addr) error {
	// 1. Refuse what needs no storage: a malformed email or no client.
	email, err := emailaddress.Parse(email)
	if err != nil {
		return err
	}
	if !client.IsValid() {
		return errClientAddr
	}

	// 2. Under the client's lock, check its limit, record the attempt, and
	// save the job that sends its link.
	since := r.now().UTC().Add(-attemptRetention)
	return r.storage.RecordPasswordResetRequest(ctx, clientprefix.Of(client).String(), email, since, func(times []time.Time) (time.Time, error) {
		// Read under the lock, so a request that waited counts from now.
		now := r.now().UTC()
		if wait := attemptlimit.Wait(times, now, clientLimit); wait > 0 {
			return time.Time{}, &attemptlimit.Error{Err: ErrThrottled, Wait: wait}
		}
		return now, nil
	})
}
