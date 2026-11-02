package postgres

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/account"
	"inbok/internal/registration"
)

// registeredEmail is the address a visitor registers in these tests.
const registeredEmail = "visitor@example.org"

// register completes a registration link to email with deactivatedPassword
// and returns the new member.
func register(t *testing.T, f deactivatedAccounts, email string) account.User {
	t.Helper()

	secret, _ := saveRegistrationLink(t, f.conn, email)
	completion := registration.NewCompletion(f.db, func() time.Time { return requestNow })
	user, err := completion.Complete(t.Context(), secret, deactivatedPassword)
	require.NoError(t, err)
	return user
}

// A registered member is a member like any other: listed, deactivated, told
// so at sign-in, and reactivated, and the three events stay apart.
func TestRegisteredMemberDeactivation(t *testing.T) {
	// Given an administrator with admin access and a member who registered.
	f := newDeactivatedAccounts(t, false)
	admin, send := deactivatedAPI(t, f)
	openRegistration(t, f.conn)
	registered := register(t, f, registeredEmail)
	path := "/members/" + strconv.FormatInt(registered.ID, 10)
	signIn := `{"email":"` + registeredEmail + `","password":"` + deactivatedPassword + `"}`

	// When the administrator lists members and deactivates the registrant,
	// who signs in, then reactivates them, who signs in again.
	listed := send(http.MethodGet, "/members", "", admin)
	deactivated := send(http.MethodPost, path+"/deactivate", "", admin)
	refused := send(http.MethodPost, "/sessions", signIn, nil)
	reactivated := send(http.MethodPost, path+"/reactivate", "", admin)
	signedIn := send(http.MethodPost, "/sessions", signIn, nil)

	// Then the list shows a verified member, the deactivated sign-in learns
	// why, and the reactivated one succeeds.
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var body struct {
		Data struct {
			Members []struct {
				ID            int64  `json:"id"`
				Role          string `json:"role"`
				EmailVerified bool   `json:"emailVerified"`
			} `json:"members"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &body))
	require.Len(t, body.Data.Members, 3)
	assert.Equal(t, registered.ID, body.Data.Members[2].ID)
	assert.Equal(t, "member", body.Data.Members[2].Role)
	assert.True(t, body.Data.Members[2].EmailVerified)
	require.Equal(t, http.StatusOK, deactivated.Code, deactivated.Body.String())
	assert.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), `"type":"/api/v0/problems/account-deactivated"`)
	require.Equal(t, http.StatusOK, reactivated.Code, reactivated.Body.String())
	assert.Equal(t, http.StatusCreated, signedIn.Code, signedIn.Body.String())
	assert.Equal(t, []auditEvent{
		{Action: "member-registered", Target: &registered.ID},
		memberEvent("member-deactivated", f.admin, registered.ID),
		memberEvent("member-reactivated", f.admin, registered.ID),
	}, auditEvents(t, f.conn))
}

// A deactivated account still has its address, so asking to register it
// emails the notice, as for any account; the answer must not depend on the
// account's state.
func TestRegistrationRequestDeactivated(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	openRegistration(t, f.conn)
	_, err := f.members.Deactivate(t.Context(), f.admin, f.member)
	require.NoError(t, err)
	mailer := &verificationMailer{}
	requests := registration.NewRequests(f.db, mailer, &url.URL{Scheme: "https", Host: "mail.example.com"}, func() time.Time { return requestNow })

	require.NoError(t, requests.Send(t.Context(), "member@example.com", netip.MustParseAddr("192.0.2.1")))

	assert.Len(t, mailer.bodies, 1)
	assert.Empty(t, registrationLinkHashes(t, f.conn))
	var outcome string
	require.NoError(t, f.conn.QueryRow(t.Context(), "SELECT outcome FROM registration_attempts").Scan(&outcome))
	assert.Equal(t, "notice", outcome)
}

// A deactivation waits for a completion that holds the users table lock:
// its UPDATE needs ROW EXCLUSIVE, which SHARE ROW EXCLUSIVE blocks. The
// completion never waits for the deactivation, so both finish.
func TestDeactivationWaitsForCompletion(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	openRegistration(t, f.conn)
	_, hash := saveRegistrationLink(t, f.conn, registeredEmail)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	finish := holdCompletion(t, ctx, f.db, hash, registeredEmail)
	deactivated := make(chan error, 1)
	go func() {
		_, err := f.members.Deactivate(ctx, f.admin, f.member)
		deactivated <- err
	}()
	waitForLock(t, ctx, f.db, "the deactivation did not wait for the completion")

	require.NoError(t, finish())
	require.NoError(t, <-deactivated)
	var actions []string
	for _, event := range auditEvents(t, f.conn) {
		actions = append(actions, event.Action)
	}
	assert.Equal(t, []string{"member-registered", "member-deactivated"}, actions)
}

// A completion does not wait for a deactivation that holds a member's row
// lock: FOR UPDATE takes ROW SHARE on the table, which SHARE ROW EXCLUSIVE
// allows.
func TestCompletionPassesMemberLock(t *testing.T) {
	f := newDeactivatedAccounts(t, false)
	openRegistration(t, f.conn)
	_, hash := saveRegistrationLink(t, f.conn, registeredEmail)
	ctx, cancel := context.WithTimeout(t.Context(), setupTestTimeout)
	defer cancel()

	// Given a deactivation that holds the member's row lock.
	locked, release, deactivated := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	go func() {
		_, err := f.db.UpdateMember(ctx, f.admin, f.member, func(account.Member) (account.MemberChange, error) {
			close(locked)
			<-release
			return account.MemberChange{Action: account.MemberDeactivate, At: requestNow}, nil
		})
		deactivated <- err
	}()
	<-locked

	// When a completion runs meanwhile, then it commits first.
	_, err := f.db.CompleteRegistration(ctx, hash, registeredEmail, func(registration.CompleteState) (registration.CompleteChange, error) {
		return registration.CompleteChange{PasswordHash: "unused", VerifiedAt: requestNow}, nil
	})
	require.NoError(t, err, "the completion waited for the member's row lock")
	releaseOnce()
	require.NoError(t, <-deactivated)
}

// Completions racing deactivations never deadlock: PostgreSQL would abort
// one side with an error.
func TestRegistrationDeactivationRace(t *testing.T) {
	const rounds = 15
	f := newDeactivatedAccounts(t, false)
	openRegistration(t, f.conn)
	completion := registration.NewCompletion(f.db, func() time.Time { return requestNow })
	// A completion spends most of its time hashing the password before its
	// transaction. Every other round deactivates at once; the rest wait a
	// random time of up to twice a completion, landing before, during, or
	// after its transaction.
	started := time.Now()
	register(t, f, "timing@example.org")
	window := 2 * time.Since(started)

	for round := range rounds {
		_, err := f.members.Reactivate(t.Context(), f.admin, f.member)
		require.NoError(t, err)
		secret, _ := saveRegistrationLink(t, f.conn, "visitor"+strconv.Itoa(round)+"@example.org")
		var delay time.Duration
		if round%2 == 1 {
			delay = rand.N(window)
		}
		var wg sync.WaitGroup
		var completeErr, deactivateErr error
		wg.Go(func() {
			_, completeErr = completion.Complete(t.Context(), secret, deactivatedPassword)
		})
		wg.Go(func() {
			time.Sleep(delay)
			_, deactivateErr = f.members.Deactivate(t.Context(), f.admin, f.member)
		})
		wg.Wait()

		require.NoError(t, completeErr, "round %d", round)
		require.NoError(t, deactivateErr, "round %d", round)
	}
}
