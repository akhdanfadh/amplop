package totp

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"strconv"
	"time"

	"inbok/internal/account"
	"inbok/internal/randomsecret"
)

// State identifies whether a factor is absent, being set up, past its enrollment
// time, or confirmed.
type State string

const (
	StateOff     State = "off"
	StatePending State = "pending"
	StateExpired State = "expired"
	StateOn      State = "on"
)

// enrollmentLifetime bounds an unfinished enrollment from its start, and a waiting
// replacement set from the code check that made it. Product policy: long
// enough to scan a code and save the backup codes, short enough that a
// forgotten enrollment does not stay open.
const enrollmentLifetime = 15 * time.Minute

var (
	// ErrAlreadyOn prevents replacing a confirmed authenticator through enrollment.
	ErrAlreadyOn = errors.New("TOTP is already on")

	// ErrNotPending means no unfinished enrollment exists.
	ErrNotPending = errors.New("TOTP enrollment is not pending")

	// ErrEnrollmentExpired means the unfinished enrollment started [enrollmentLifetime] or
	// more ago and can no longer be finished.
	ErrEnrollmentExpired = errors.New("TOTP enrollment expired")

	// ErrCodeChecked means the enrollment's code was already checked, so its
	// backup codes wait to be saved.
	ErrCodeChecked = errors.New("TOTP enrollment code already checked")

	// ErrNotWaiting means no live backup-code set waits for this token.
	ErrNotWaiting = errors.New("no backup codes waiting")
)

// Factor is the encrypted factor, the last accepted step, and when its enrollment
// started, saved by storage.
type Factor struct {
	KeyID       string
	Ciphertext  []byte
	ConfirmedAt *time.Time
	LastStep    int64
	StartedAt   time.Time
}

// CheckEnrollment returns [ErrEnrollmentExpired] for an unconfirmed factor whose enrollment
// started [enrollmentLifetime] or more before now, and nil otherwise.
func CheckEnrollment(factor Factor, now time.Time) error {
	if factor.ConfirmedAt == nil && !now.Before(factor.StartedAt.Add(enrollmentLifetime)) {
		return ErrEnrollmentExpired
	}
	return nil
}

// Waiting is a backup-code set that waits for the person to say they saved
// it: the hashes that become active, the hash of the one-time token that
// activates it, and, for an enrollment's set only, an encrypted display
// copy; a replacement set's KeyID and Ciphertext are empty.
type Waiting struct {
	Hashes     []string
	KeyID      string
	Ciphertext []byte
	TokenHash  [sha256.Size]byte
	ExpiresAt  time.Time
}

// checkWaiting returns [ErrNotWaiting] unless waiting is present and not
// expired at now.
func checkWaiting(waiting *Waiting, now time.Time) error {
	if waiting == nil || !now.Before(waiting.ExpiresAt) {
		return ErrNotWaiting
	}
	return nil
}

// VerifyWaiting returns [ErrNotWaiting] unless waiting is present, not
// expired at now, and token is its one-time token.
func VerifyWaiting(waiting *Waiting, token string, now time.Time) error {
	if err := checkWaiting(waiting, now); err != nil {
		return err
	}
	hash, err := randomsecret.Hash(token)
	if err != nil || hash != waiting.TokenHash {
		return ErrNotWaiting
	}
	return nil
}

// Record is a user's factor and waiting set, loaded with the owner locked.
// Either can be nil.
type Record struct {
	Factor  *Factor
	Waiting *Waiting
}

// WaitingWrite is what a [Change] does to the waiting backup-code set.
type WaitingWrite int

const (
	// KeepWaiting leaves the waiting set as it is.
	KeepWaiting WaitingWrite = iota
	// DeleteWaiting deletes the waiting set.
	DeleteWaiting
	// SaveWaiting saves [Change.Waiting], replacing any waiting set.
	SaveWaiting
)

// Change is one write to a user's factor. Storage applies, in this order,
// each field that is set, all in one transaction.
type Change struct {
	// Factor, when set, saves the factor.
	Factor *Factor
	// WaitingWrite keeps, deletes, or saves the waiting set; Waiting is the
	// set [SaveWaiting] saves and is nil otherwise.
	WaitingWrite WaitingWrite
	Waiting      *Waiting
	// BackupHashes, when not nil, replaces every active backup code.
	BackupHashes []string
	// AttemptID, when not zero, removes the owner's successful attempt.
	AttemptID int64
}

// Storage serializes factor changes, including when a user has no factor yet.
type Storage interface {
	// ReadTOTPStatus returns metadata and count from one snapshot, nil if absent.
	ReadTOTPStatus(ctx context.Context, userID int64) (*StatusRecord, error)

	// UpdateTOTP locks the user, loads the factor and waiting set, calls
	// update, and saves its [Change] in one transaction. An error rolls back
	// every write. A missing or deactivated user returns
	// [account.ErrUserNotFound].
	UpdateTOTP(ctx context.Context, userID int64, update func(Record) (Change, error)) error

	// CancelTOTP locks the owner, checks the current factor, and deletes it,
	// with its waiting set, in one transaction. A missing factor is passed to
	// check as nil. A missing or deactivated user returns
	// [account.ErrUserNotFound].
	CancelTOTP(ctx context.Context, userID int64, check func(*Factor) error) error
}

type sealer interface {
	Seal(plaintext []byte, owner string) (string, []byte, error)
	Open(keyID string, ciphertext []byte, owner string) ([]byte, error)
}

type passwordVerifier interface {
	VerifyPassword(ctx context.Context, user account.User, password string, addr netip.Addr) error
}

// Service manages authenticator enrollment and status.
type Service struct {
	storage    Storage
	encryption sealer
	passwords  passwordVerifier
	verifier   *Verifier
	throttle   codeAttempts
	now        func() time.Time
}

// NewService supplies storage, owner-bound encryption for saved secrets, the
// password check that guards showing them, the code verifier and attempt
// limit, and a clock.
func NewService(storage Storage, encryption sealer, passwords passwordVerifier, verifier *Verifier, throttle codeAttempts, now func() time.Time) *Service {
	return &Service{storage: storage, encryption: encryption, passwords: passwords, verifier: verifier, throttle: throttle, now: now}
}

// Started is the enrollment data shown to the user after storage commits.
type Started struct {
	Secret string
	URI    string
}

// Status contains only display data, never the stored secret.
type Status struct {
	State              State
	BackupCodesLeft    int
	BackupCodesWaiting bool
}

// StatusRecord contains stored display metadata, without secrets or hashes.
// WaitingExpiresAt is nil when no set waits.
type StatusRecord struct {
	ConfirmedAt      *time.Time
	StartedAt        time.Time
	BackupCodesLeft  int
	WaitingExpiresAt *time.Time
}

// Start checks user's password, then starts an enrollment, or continues an
// unfinished one by returning its secret again. An expired enrollment is
// replaced. It returns [ErrCodeChecked] once the enrollment's code was checked,
// [ErrAlreadyOn] for a confirmed factor, and the password check's errors.
func (s *Service) Start(ctx context.Context, user account.User, password string, addr netip.Addr) (Started, error) {
	// 1. Check the password before any secret is made or shown: a session
	// cookie alone must not reveal or add an authenticator.
	if err := s.passwords.VerifyPassword(ctx, user, password, addr); err != nil {
		return Started{}, err
	}

	// 2. Create and seal a secret before storage takes its lock, in case
	// there is no enrollment to continue.
	fresh, err := newEnrollment(user.Email)
	if err != nil {
		return Started{}, err
	}
	owner := secretOwner(user.ID)
	keyID, ciphertext, err := s.encryption.Seal([]byte(fresh.secret), owner)
	if err != nil {
		return Started{}, err
	}

	// 3. Under the owner's lock, continue a live enrollment or save the new one.
	var started Started
	err = s.storage.UpdateTOTP(ctx, user.ID, func(current Record) (Change, error) {
		now := s.now().UTC()
		if factor := current.Factor; factor != nil {
			if factor.ConfirmedAt != nil {
				return Change{}, ErrAlreadyOn
			}
			if CheckEnrollment(*factor, now) == nil {
				if current.Waiting != nil {
					return Change{}, ErrCodeChecked
				}
				secret, err := s.encryption.Open(factor.KeyID, factor.Ciphertext, owner)
				if err != nil {
					return Change{}, err
				}
				existing, err := existingEnrollment(user.Email, string(secret))
				if err != nil {
					return Change{}, err
				}
				started = Started{Secret: existing.secret, URI: existing.uri}
				return Change{}, nil
			}
		}

		started = Started{Secret: fresh.secret, URI: fresh.uri}
		factor := Factor{KeyID: keyID, Ciphertext: ciphertext, LastStep: -1, StartedAt: now}
		return Change{Factor: &factor, WaitingWrite: DeleteWaiting}, nil
	})
	if err != nil {
		return Started{}, err
	}
	return started, nil
}

// Codes is a waiting backup-code set to show, and the one-time token that
// activates it.
type Codes struct {
	Codes []string
	Token string
}

// VerifyCode verifies a code from the unfinished enrollment's authenticator, then
// makes the backup codes that wait for the person to save them. Two-factor
// stays off. It returns [ErrNotPending], [ErrAlreadyOn], [ErrEnrollmentExpired],
// [ErrCodeChecked], [ErrInvalidCode], or the attempt limit's error.
func (s *Service) VerifyCode(ctx context.Context, userID int64, code string) (Codes, error) {
	// 1. Reserve an attempt before any code is verified.
	attemptID, err := s.throttle.Begin(ctx, userID)
	if err != nil {
		return Codes{}, err
	}

	// 2. Make the codes and seal their display copy before taking the
	// owner's write lock: salted hashes are slow.
	set, err := newWaitingSet(ctx)
	if err == nil {
		err = set.sealCopy(s.encryption, userID)
	}
	if err != nil {
		// No code was verified.
		s.throttle.Release(ctx, attemptID)
		return Codes{}, err
	}

	verified := false
	err = s.storage.UpdateTOTP(ctx, userID, func(current Record) (Change, error) {
		// 3. Under the owner's lock, refuse anything but a live enrollment whose
		// code is unchecked.
		factor := current.Factor
		if factor == nil {
			return Change{}, ErrNotPending
		}
		if factor.ConfirmedAt != nil {
			return Change{}, ErrAlreadyOn
		}
		if err := CheckEnrollment(*factor, s.now()); err != nil {
			return Change{}, err
		}
		if current.Waiting != nil {
			return Change{}, ErrCodeChecked
		}

		// 4. Decrypt the secret, bound to this owner, and verify the code.
		secret, err := s.encryption.Open(factor.KeyID, factor.Ciphertext, secretOwner(userID))
		if err != nil {
			return Change{}, err
		}
		verified = true
		step, err := s.verifier.Verify(string(secret), code, factor.LastStep)
		if err != nil {
			return Change{}, err
		}

		// Describe the write without mutating the loaded factor before
		// commit. The codes wait only as long as the enrollment lives.
		next := *factor
		next.LastStep = step
		return Change{
			Factor: &next, WaitingWrite: SaveWaiting, Waiting: set.waiting(factor.StartedAt.Add(enrollmentLifetime)),
			AttemptID: attemptID,
		}, nil
	})

	// 5. Release the attempt when no code was verified. A request refused
	// before that, e.g. with nothing pending, made no guess.
	if err != nil {
		if !verified {
			s.throttle.Release(ctx, attemptID)
		}
		return Codes{}, err
	}

	// 6. Return the codes and token only after they are saved, so a lost
	// response can be shown again.
	return set.codes(), nil
}

// ShowWaiting checks user's password, then returns an unfinished
// enrollment's waiting backup codes again with a fresh one-time token, which
// replaces the earlier one. It returns [ErrAlreadyOn] once two-factor is on,
// [ErrEnrollmentExpired] for an expired enrollment, [ErrNotWaiting] when no
// live set waits, and the password check's errors.
//
// DESIGN: only an enrollment's codes are shown again. While two-factor is
// off the password is the account's only proof, so showing them behind it
// gives no one more than the password already does. Once it is on, new codes
// appear only in the answer to [Backups.Replace], which proved the
// authenticator: showing them behind the password alone would let someone
// with a stolen session cookie and password take over the backup codes.
func (s *Service) ShowWaiting(ctx context.Context, user account.User, password string, addr netip.Addr) (Codes, error) {
	// 1. Check the password before showing codes, as for the enrollment secret.
	if err := s.passwords.VerifyPassword(ctx, user, password, addr); err != nil {
		return Codes{}, err
	}

	token, tokenHash := randomsecret.New()
	var codes []string
	err := s.storage.UpdateTOTP(ctx, user.ID, func(current Record) (Change, error) {
		// 2. Under the owner's lock, refuse a confirmed factor, an expired
		// enrollment, or a set that ended or has no copy to show.
		now := s.now()
		if current.Factor == nil {
			return Change{}, ErrNotWaiting
		}
		if current.Factor.ConfirmedAt != nil {
			return Change{}, ErrAlreadyOn
		}
		if err := CheckEnrollment(*current.Factor, now); err != nil {
			return Change{}, err
		}
		waiting := current.Waiting
		if err := checkWaiting(waiting, now); err != nil {
			return Change{}, err
		}
		if len(waiting.Ciphertext) == 0 {
			return Change{}, ErrNotWaiting
		}

		// 3. Decrypt the display copy and replace the token.
		plaintext, err := s.encryption.Open(waiting.KeyID, waiting.Ciphertext, codesOwner(user.ID))
		if err != nil {
			return Change{}, err
		}
		codes, err = parseDisplayCodes(plaintext)
		if err != nil {
			return Change{}, err
		}
		next := *waiting
		next.TokenHash = tokenHash
		return Change{WaitingWrite: SaveWaiting, Waiting: &next}, nil
	})
	if err != nil {
		return Codes{}, err
	}
	return Codes{Codes: codes, Token: token}, nil
}

// Cancel discards an unfinished enrollment, with any codes waiting. Repeating a
// completed cancellation is safe.
func (s *Service) Cancel(ctx context.Context, userID int64) error {
	// 1. Refuse an invalid user ID before storage.
	if userID <= 0 {
		return account.ErrUserNotFound
	}

	// 2. Delete the pending factor. Check it under the owner lock so
	// finishing the enrollment cannot race this deletion.
	return s.storage.CancelTOTP(ctx, userID, func(current *Factor) error {
		if current != nil && current.ConfirmedAt != nil {
			return ErrAlreadyOn
		}
		return nil
	})
}

// Status reports off for a missing factor, pending or expired for an
// unfinished enrollment, and on for a confirmed factor, with whether a live
// backup-code set waits to be saved.
func (s *Service) Status(ctx context.Context, userID int64) (Status, error) {
	record, err := s.storage.ReadTOTPStatus(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	if record == nil {
		return Status{State: StateOff}, nil
	}

	now := s.now()
	waiting := record.WaitingExpiresAt != nil && now.Before(*record.WaitingExpiresAt)
	if record.ConfirmedAt == nil {
		if CheckEnrollment(Factor{StartedAt: record.StartedAt}, now) != nil {
			return Status{State: StateExpired}, nil
		}
		return Status{State: StatePending, BackupCodesWaiting: waiting}, nil
	}
	return Status{State: StateOn, BackupCodesLeft: record.BackupCodesLeft, BackupCodesWaiting: waiting}, nil
}

// secretOwner binds an encrypted authenticator secret to its user.
func secretOwner(userID int64) string {
	return "totp:" + strconv.FormatInt(userID, 10)
}
