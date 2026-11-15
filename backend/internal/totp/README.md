# TOTP rules and ownership

This package generates enrollment secrets and verifies time-based codes.
The enrollment service encrypts secrets before passing them to storage.
PostgreSQL stores the encrypted factor and serializes updates by locking its
user. Enrollment has three steps, each its own request:

1. `POST /api/v0/totp` with the account's password starts an enrollment, or
   continues one started in the last 15 minutes by returning the same
   secret. Nothing secret shows without the password.
2. `POST /api/v0/totp/enrollment/code` checks a code from the new
   authenticator, then makes ten backup codes that wait, and returns them
   with a one-time token. Two-factor stays off.
3. `POST /api/v0/totp/enrollment/finish` with that token, once the person
   has saved the codes, turns two-factor on.

`GET /api/v0/totp` reads the status: `off`, `pending`, `expired`, or `on`,
with `backupCodesWaiting` when codes wait for step 3 or for a replacement.

The verifier reads a supplied clock and returns the matched 30-second step.
A successful check alone does not consume a code: the caller must save that
step atomically with its action.

## Service ownership

- `totp` owns factor state, code rules, backup-code rules, enrollment start
  and status, the code check, backup-code replacement, and member turn-off. Its service
  declares the small encryption interface it consumes. It may import
  `account` for user and role types; it never imports `session`.
- `session` already imports `account` and may import `totp`. It owns
  password sign-in, sign-in challenges, the code sign-in step, the password
  check `totp` asks for, and finishing enrollment. Finishing belongs here because
  it replaces the current session and ends the other sessions.
  Session-secret generation stays private to this package.
- `session.Enrollment` declares an update-function storage method for
  finishing: storage locks and loads the user, the current session, the
  factor, and its waiting codes, calls the feature's decision, then
  confirms the factor, makes the waiting codes active, ends other sessions,
  and replaces the current session in one transaction.
- `totp.Storage` uses the same update-function pattern for factor-only
  writes: one `UpdateTOTP` loads the factor and its waiting codes and saves
  the `Change` the feature returns. Turn-off checks the role from the locked user row. Every code
  acceptance checks and records the last step in that transaction.
- `repository/postgres` implements both storage interfaces. Feature code
  receives neither a transaction nor SQL queries.
- `httpapi` routes each operation to its owning service. `cmd/inbok` wires
  services, encryption, storage, and clocks. Both feature packages can
  share the verifier without either calling the other feature's storage.

There is no shared transaction provider or generic auth coordinator. Concrete
storage types and method signatures will be added only as each operation needs
them.

## OTP dependency

Pinned: `github.com/pquerna/otp` v1.5.0. Its release includes the fix that
uses `io.ReadFull` when generating random secrets. Maintenance is infrequent:
the repository's latest commit was dated 2025-08-07 when checked on 2026-10-29.
GitHub listed no published advisories; OSV returned no known vulnerabilities
for v1.5.0. These checks do not establish that the package has no defects.

- Source and releases: https://github.com/pquerna/otp
- Advisories: https://github.com/pquerna/otp/security/advisories
- Version query: https://api.osv.dev/v1/query
- Counter verification: https://pkg.go.dev/github.com/pquerna/otp@v1.5.0/hotp#ValidateCustom
- Test vectors: https://www.rfc-editor.org/rfc/rfc6238#appendix-B

## Code-attempt limits

`Throttle` defines the per-user limits. PostgreSQL locks the user while it
cleans that user's expired attempts, counts the rest with `attemptlimit.Wait`,
and saves a reservation only if one more fits, in one transaction. The enrollment
code check, code sign-in, backup-code replacement, and turn-off use this
limiter. The password asked before a secret shows counts against sign-in's
limits instead (`session.Service.VerifyPassword`, ADR024).

Reserve an attempt with `Begin` before checking a code. Pending checks count
against both limits, so parallel requests cannot all pass an earlier count.
Leave the attempt saved when a code fails. Success removes its own attempt,
preserving earlier failures. A request that fails before checking any code,
e.g. turn-off with TOTP off, releases its attempt with `Release`, because it
made no guess. A rejected reservation saves nothing and must not trigger code
verification.

The policy allows 5 failed or pending attempts per 15 minutes and 20 per 24
hours. Attempts exactly at a window's cutoff no longer count. TOTP codes,
backup codes, and replayed codes share the same allowance across sign-in,
the enrollment code check, backup-code replacement, and turn-off. Starting enrollment
again does not reset that allowance.

These values are product policy. OWASP recommends strict OTP attempt limits
and account-based counters but does not prescribe these two thresholds:

- https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html#one-time-password-otp-handling-and-storage
- https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#login-throttling

A blocked attempt returns `ErrThrottled` and logs a warning containing the user
ID. The limiter receives no code or secret. Every code-checking endpoint maps
this error to HTTP 429 with problem type `/api/v0/problems/totp-throttled`.

## Backup-code rules

A set contains ten distinct codes, each displayed as `xxxxx-xxxxx`. The
31-symbol alphabet omits `0`, `O`, `1`, `I`, and `L`. Generation uses uniform
cryptographic randomness. Entry ignores ASCII letter case, whitespace, and
ASCII dashes; other characters and lengths are rejected.

Backup codes serve as an alternate second factor. Their roughly 50 bits of
randomness fall below NIST's 112-bit threshold for look-up secrets, so each
code uses a fresh salt and password hashing instead of HMAC-only storage.

Account passwords and backup codes share `internal/passwordhash`. It stores
Argon2id PHC strings containing the algorithm, version, work settings, salt,
and digest. Current generation uses 19 MiB, two passes, one lane, a 16-byte
salt, and a 32-byte digest, matching OWASP's minimum Argon2id profile.
Verification reads the stored settings and bounds their cost before hashing,
so generation defaults can change without breaking existing hashes. Store
one PHC string per code; no separate profile ID is needed. Account passwords
preserve their bytes; backup codes are normalized by `totp` before hashing.

`NewBackupCodes` keeps display codes separate from storage hashes. Generate
these hashes before taking a database write lock. Return display codes only
after the operation saving their hashes commits. `VerifyBackupCode` performs
comparison only. The enrollment code check and replacement store the hashes as a
waiting set; finishing makes them active; code sign-in consumes matched rows
atomically.

Salted hashes require verifying the user's remaining hashes, up to ten, rather
than looking up a fast hash. A fast lookup digest would also let an attacker
test guesses cheaply. Call the shared limiter before verification, and delete
the matched row atomically with the action. Backup-code hashes do not depend
on deployment encryption keys.

- NIST look-up secrets: https://pages.nist.gov/800-63-4/sp800-63b.html#look-up-secret-verifiers
- OWASP Argon2id profile: https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#argon2id

## Backup-code storage

`totp_backup_codes` holds one independently salted hash per row. Each row
belongs to a factor through `user_id`; deleting the factor deletes its codes.
An index on `user_id` supports loading the remaining codes for verification.

After a hash matches, consume its row by both ID and owner with
`DELETE ... RETURNING`. No returned row means it has already been consumed
or does not belong to that user. Run this query in the protected action's
transaction: commit consumes the code, and rollback leaves it usable.
Concurrent deletion of the same row can succeed only once.

These queries are storage primitives. Feature operations will use them inside
their update-function storage methods; handlers never call SQL queries.

## Enrollment cancellation

`Service.Cancel` discards an unfinished enrollment, with any codes waiting. An absent factor is
already canceled, so repeating the request succeeds. A confirmed factor
returns `ErrAlreadyOn`; turning off an active authenticator requires the
separate proof-checked operation.

Storage locks the owner before loading the factor and calling the service's
check. It deletes the factor only after that check succeeds, in the same
transaction. This shares the lock used by start and finishing: if
finishing wins, cancellation cannot remove the active authenticator;
if cancellation wins, finishing finds no unfinished enrollment. Existing
sessions and code-attempt records stay unchanged. Cancellation needs no code
or encryption key because it cannot remove a confirmed authenticator.

`DELETE /api/v0/totp/enrollment` exposes cancellation to signed-in members
and administrators, including a session with enrollment access. It takes no
body and selects the owner from the checked session. HTTP 200 returns
`data.status: "off"`, `data.backupCodesLeft: 0`, and
`data.backupCodesWaiting: false`, including repeated cancellation. A confirmed authenticator returns HTTP 409
(`totp-already-on`); a missing account returns HTTP 401. Responses use
`Cache-Control: no-store` and do not replace the session cookie.

## Enrollment steps

### Start or continue

`Service.Start` first checks the account's password through
`session.Service.VerifyPassword`, which reserves a sign-in attempt for the
account's email and the client's address (ADR024). A wrong password keeps
the attempt, so a stolen session cookie cannot guess it freely. Then, under
the owner's lock:

- no factor, or an unfinished enrollment started 15 minutes ago or more: save a
  new secret with `started_at` now, and delete any waiting codes, which
  belonged to the old secret;
- an unfinished enrollment younger than that, with no waiting codes: return its
  secret and URI again and write nothing, so the clock does not restart;
- an unfinished enrollment whose code was checked: `ErrCodeChecked`, because its
  codes wait to be shown (`ShowWaiting`);
- a confirmed factor: `ErrAlreadyOn`.

The 15 minutes are product policy (`enrollmentLifetime`): long enough to scan a
code and save the codes, short enough that a forgotten enrollment cannot be
finished later. One clock runs from the start; continuing or checking the
code does not reset it. An expired enrollment stays stored until the next
start replaces it or cancellation deletes it, so the page can say it
expired; its secret can no longer become active. The same holds for a
waiting set past its expiry: it stays until the member's next write
replaces or deletes it, and every path that reads it checks the expiry
first, so nothing stored can be shown or activated. Deleting expired rows
on a schedule is a later cleanup job's (ADR032).

### Code check

`Service.VerifyCode` reserves a code attempt and makes ten backup codes, a
sealed display copy, and a one-time token before taking the owner's lock.
Under it, a live enrollment whose code is unchecked accepts a code from its
secret. The change saves the accepted step and the waiting set in
`totp_waiting_backup_codes`: the hashes, the display copy, the token's
SHA-256 hash, and the enrollment's own expiry. It deletes only the
successful attempt. Two-factor stays off: the codes do not work yet.

The display copy is encrypted like the secret, with the owner string
`totp-backup-codes:<user id>`, so neither ciphertext opens as the other. It
lets a reload, or a lost answer, show the same codes again. Active codes are
only ever stored hashed (NIST look-up secrets): finishing deletes the copy.
Only an enrollment's set has a copy; a replacement's keeps none (see
"Backup-code replacement"), and the table holds its key and ciphertext both
or neither.

`POST /api/v0/totp/enrollment/code` answers HTTP 200 with
`data.backupCodes` and `data.token`. A wrong, malformed, or reused code is
HTTP 422 (`invalid-totp-code`) at `body.code`. No enrollment, an expired one, a
checked one, and a confirmed factor are distinct HTTP 409 problems
(`totp-not-pending`, `totp-enrollment-expired`, `totp-code-checked`,
`totp-already-on`); throttling is HTTP 429 (`totp-throttled`).

### Showing waiting codes again

`Service.ShowWaiting` checks the password as `Start` does, then decrypts an
unfinished enrollment's display copy and replaces the token with a fresh
one. Only the newest token finishes, so a second tab that shows the codes
takes over. `POST /api/v0/totp/backup-codes/waiting` serves enrollment only:
nothing waiting is HTTP 409 (`backup-codes-not-waiting`), an expired
enrollment HTTP 409 (`totp-enrollment-expired`), and two-factor on HTTP 409
(`totp-already-on`).

Only an enrollment's codes are shown again. While two-factor is off, the
password is the account's only proof, so showing them behind it gives no one
more than the password already does. Once it is on, new codes appear only
in the answer to `Backups.Replace`, which proved the authenticator; showing
them behind the password alone would let someone with a stolen session
cookie and password take over the backup codes, and with them sign-in.

### Finishing

`session.Enrollment.Finish` takes the token. Under the owner's lock, then
the session's, it rechecks the session, refuses anything but a live enrollment
whose codes wait for this token, and saves: the factor confirmed, the
waiting hashes as the active codes, the waiting set deleted, every session
of the user ended, and a new full-length session, in one transaction. The
token is 256 random bits, so finishing needs no attempt limit.

The token proves this browser received the codes, which needed the
enrollment's secret, which needed the password; a stolen session cookie
alone cannot turn on someone else's enrollment. A lost answer loses nothing secret: either
nothing changed and the same request works again, or two-factor is on and
the person already holds the codes.

`POST /api/v0/totp/enrollment/finish` sets the replacement session cookie
and returns `data.status: "on"` and `data.backupCodesLeft`, the count of the
codes it activated. Errors never return a cookie.

### Status

Session identity grants admin access only to an administrator with
confirmed TOTP. An administrator without it gets enrollment access, which
allows only the current session, sign-out, and the enrollment operations
(status, start, code check, waiting codes, finish, cancel), and
`totpRequired: true` (ADR020). Members keep
member access and `totpRequired: false` regardless of TOTP. Identity reads
account and factor metadata through the session row in one snapshot,
rejecting proof revoked since the earlier lifetime check.

`GET /api/v0/totp` reads confirmation, start time, the remaining
backup-code count, and the waiting set's expiry in one database snapshot. It
reports usable codes only for a confirmed factor. Status reads load neither
the encrypted secret nor backup-code hashes.

## Sign-in challenge rules

`session.Challenge` represents password proof awaiting a second factor. It
contains a hash of a fresh 32-byte random secret, the user ID, a fixed
five-minute expiry, and a wrong-code count. The secret uses the same format
as a session secret; separate storage tables keep their authority apart.
A challenge is never an authenticated session.

Five minutes and five wrong codes are product policy. The check rejects a
challenge at its expiry or after its fifth failure. Retrying does not extend
its lifetime or reset failures. Storage must check and update it under one
lock; the code operation must delete it after five wrong codes or success.

The data, check rules, and PostgreSQL storage queries are implemented.
`sign_in_challenges` stores only the hash, owner, expiry, and failure count;
it has no row in `sessions`. Deleting an account removes its challenges.
A user-and-expiry index supports pruning that user's expired challenges.

The owner lookup does not authorize a request. Code verification must lock
the user first, then reload the owned challenge with `FOR UPDATE`. Counter
changes and challenge deletion belong in that transaction. Deletion uses
`RETURNING`, so concurrent consumers can succeed only once; rollback keeps
the challenge usable. The expiry cleanup query uses `expires_at <= cutoff`
and never removes another user's challenges.

`session.Service.StartSignIn` calls `StartSignIn`, whose transaction locks the
owner and reads the current factor before choosing a session or challenge.
It also prunes that user's expired challenges. This shares enrollment's lock
order: a password-only session created first is revoked by finishing enrollment;
a sign-in waiting behind finishing sees the confirmed factor instead. The
lock also reads the role: an administrator without a confirmed factor gets
a session that lives 15 minutes, like an enrollment, and has enrollment
access only; finishing enrollment replaces it with a full session.
First-admin setup signs its new administrator in the same way
(`session.Service.SignInFirstAdmin`).

`session.CodeSignIn.Complete` locates the challenge owner, reserves a shared
code attempt, and calls `CompleteSignIn`. Its locked callback checks expiry,
failure count, and the current confirmed factor. Six ASCII digits select
TOTP; other inputs try the remaining salted backup hashes. A wrong code
commits its counter before returning an error. The fifth wrong code or an
expired challenge commits deletion before asking for password sign-in again.

Success saves the new session while consuming the challenge and the matched
TOTP step or backup row. It removes the successful attempt in that same
transaction. Storage failure rolls back all changes and returns no cookie
secret. Earlier failed attempts remain counted.

`POST /api/v0/sessions` returns `step: "totp"` and a challenge cookie for a
confirmed factor, clearing any session cookie. `POST /api/v0/sessions/totp`
accepts `code`; success returns `step: "complete"`, sets a session cookie,
and clears the challenge cookie. Missing, expired, consumed, or exhausted
proof returns HTTP 401 and clears the challenge cookie. A wrong code returns
HTTP 422 with `body.code`; shared throttling returns HTTP 429. Retryable
errors preserve the challenge. Neither endpoint logs codes or cookie secrets.

## Backup-code replacement

Replacement has the same two steps as an enrollment's codes. `Backups.Replace`
takes a checked user's ID and a fresh authenticator code; a backup code
cannot replace the backup codes. It reserves a code attempt and makes the new
set before the owner's lock, then saves the accepted step and the set as
waiting, for 15 minutes, replacing any earlier waiting set. The old codes
keep working. The set keeps no display copy: the codes appear only in
`Replace`'s answer, so a reload or a lost answer needs a fresh code and a new
set (see "Showing waiting codes again" for why). `Backups.Finish` takes the
set's token and swaps the waiting hashes in for the active ones. Missing or
pending factors return `ErrNotOn`; a wrong, expired, or replaced token
`ErrNotWaiting`.

`POST /api/v0/totp/backup-codes` with `code` answers HTTP 200 with
`data.backupCodes` and `data.token`; `POST /api/v0/totp/backup-codes/finish`
with `token` answers `data.status: "on"` and the new count. Neither changes
session cookies. A waiting set that expires is ignored and replaced by the
next one; turning two-factor off deletes it with the factor.

## Turning TOTP off

`Disabler.Disable` takes a checked user's ID and either a fresh authenticator
code or an unused backup code. The repository locks the user and loads the
current role with the factor and remaining backup hashes. Only the member
role is allowed; administrators and unknown roles return `ErrDisableForbidden`.
A missing or pending factor returns `ErrNotOn`.

The role is checked inside the transaction, so a concurrent promotion cannot
make a stale member check authorize deletion. Six ASCII digits select TOTP;
other input is tried as a normalized backup code. Backup proof does not need
the authenticator's encryption key.

After proof succeeds, one transaction deletes the factor and its backup codes
through the foreign-key cascade, including any backup code used as proof.
It also removes only the successful attempt. Earlier failures and all existing
sessions remain. An error, including commit failure, rolls everything back.
The owner lock serializes proof checks with sign-in and replacement.

Signed-in members can call `POST /api/v0/totp/disable` with `code`. HTTP 200
returns `data.status: "off"` and `data.backupCodesLeft: 0`, without changing
session cookies. The response uses `Cache-Control: no-store`.

Administrators receive HTTP 403 (`totp-disable-forbidden`), based on the role
loaded by the transaction. Missing or pending TOTP returns HTTP 409
(`totp-not-on`). Invalid or reused proof returns HTTP 422 (`invalid-totp-proof`)
with field location `body.code`; shared throttling returns HTTP 429
(`totp-throttled`). Unexpected errors use the shared safe 500 response.
