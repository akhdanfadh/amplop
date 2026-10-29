# TOTP rules and ownership

This package generates enrollment secrets and verifies time-based codes.
The enrollment service encrypts secrets before passing them to storage.
Starting enrollment creates a pending factor; restarting replaces only a
pending factor. PostgreSQL stores the encrypted factor and serializes
updates by locking its user. Signed-in users can start or restart enrollment
with `POST /api/v0/totp` and read status with `GET /api/v0/totp`. Starting
enrollment does not activate TOTP. Confirm with `POST /api/v0/totp/confirm`
and a code from the pending authenticator.

The verifier reads a supplied clock and returns the matched 30-second step.
A successful check alone does not consume a code: the caller must save that
step atomically with its action.

## Planned service ownership

- `totp` owns factor state, code rules, backup-code rules, enrollment start
  and status, backup-code regeneration, and member turn-off. Its service
  declares the small encryption interface it consumes. It may import
  `account` for user and role types; it never imports `session`.
- `session` already imports `account` and may import `totp`. It owns
  password sign-in, sign-in challenges, the code sign-in step, and enrollment
  confirmation. Confirmation belongs here because it replaces the current
  session and ends the other sessions. Session-secret generation stays
  private to this package.
- `session.Enrollment` declares an update-function storage method for confirmation:
  storage locks and loads the factor, user, and current session, calls the
  feature's decision, then saves the factor and backup codes, ends other
  sessions, and replaces the current session in one transaction.
- `totp.Storage` uses the same update-function pattern for factor-only
  writes. Turn-off checks the role from the locked user row. Every code
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
cleans that user's expired attempts, inserts a reservation, and checks counts
in one transaction. Enrollment confirmation uses this limiter; the other
code-checking endpoints are not implemented yet.

Reserve an attempt with `Begin` before checking a code. Pending checks count
against both limits, so parallel requests cannot all pass an earlier count.
Leave the attempt saved on failure; call `Succeed` only after the protected
action succeeds. Success removes its own attempt, preserving earlier failures.
A rejected reservation rolls back and must not trigger code verification.

The policy allows 5 failed or pending attempts per 15 minutes and 20 per 24
hours. Attempts exactly at a window's cutoff no longer count. TOTP codes,
backup codes, and replayed codes will share the same allowance across sign-in,
enrollment confirmation, backup-code regeneration, and turn-off. Restarting
enrollment will not reset that allowance.

These values are product policy. OWASP recommends strict OTP attempt limits
and account-based counters but does not prescribe these two thresholds:

- https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html#one-time-password-otp-handling-and-storage
- https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html#login-throttling

A blocked attempt returns `ErrThrottled` and logs a warning containing the user
ID. The limiter receives no code or secret. Enrollment confirmation maps this
error to HTTP 429 with problem type `/api/v0/problems/totp-throttled`.

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
after the operation saving their hashes commits. `CheckBackupCode` performs
comparison only. Confirmation and regeneration store the hashes; code sign-in
consumes matched rows atomically.

Salted hashes require checking the user's remaining hashes, up to ten, rather
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

## Enrollment confirmation rules

`session.Enrollment` owns confirmation because it must generate a replacement
session secret. It reserves a code attempt, prepares the ten backup hashes
before taking a write lock, and asks storage to confirm in one transaction.
Inside the storage callback it rechecks the current session and verifies a
TOTP code against the pending factor. A missing factor, a confirmed factor,
or an invalid or replayed code leaves enrollment unchanged.

The returned change activates the factor, records the accepted step, supplies
backup hashes, and replaces every existing session with the new one. This
ends other password-only sessions before they could gain administrator access.
The same transaction must delete only the successful attempt's reservation.
Do not run `Succeed` afterward: a cleanup error after commit could hide the
new cookie and the only copy of the backup codes from the user.

Confirmation returns the cookie secret and display codes only after storage
commits. PostgreSQL locks the owner before reading the factor and the owned
session, matching enrollment restart and attempt reservation's lock order.
It saves the whole change in one transaction. The `sessions(user_id)` index
supports ending the old sessions without scanning every user's sessions.

`POST /api/v0/totp/confirm` accepts a string `code`, preserving leading zeros.
It uses the checked session from access middleware. A successful response
sets a replacement session cookie and returns `data.backupCodes` once.
Account-specific responses use `Cache-Control: no-store`. Errors never
return a replacement cookie or backup codes.

An invalid, expired, or reused code returns HTTP 422 (`invalid-totp-code`)
with the field location `body.code`. Missing enrollment and already confirmed
enrollment return distinct HTTP 409 problems. The shared per-user limiter
returns HTTP 429 (`totp-throttled`). A session revoked between middleware and
the confirmation transaction returns HTTP 401.

Session identity grants admin access only to an administrator with confirmed
TOTP. Administrators without it keep member access and `totpRequired: true`.
Members keep member access and `totpRequired: false` regardless of TOTP.
Identity reads account and factor metadata through the session row in one
snapshot, rejecting proof revoked since the earlier lifetime check.
`GET /api/v0/totp` reads confirmation metadata and the remaining backup-code
count in one database snapshot. It reports usable codes only for a confirmed
factor. Status reads load neither the encrypted secret nor backup-code hashes.


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
order: a password-only session created first is revoked by confirmation;
a sign-in waiting behind confirmation sees the confirmed factor instead.

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
The browser code-entry screen is not implemented yet.

## Backup-code regeneration

`Backups.Regenerate` takes a checked user's ID and a fresh authenticator
code. Backup codes cannot authorize regeneration. Missing or pending factors
return `ErrNotOn`; invalid or reused TOTP codes return `ErrInvalidCode`.

The service reserves a shared code attempt and prepares ten salted hashes
before taking a write lock. PostgreSQL locks the owner, loads the latest
factor, and calls the service's decision. The transaction records the accepted
step, replaces the entire backup set, and deletes only the successful attempt.
A failure rolls back every write, preserving the old set and replay state.
Earlier failed attempts remain counted, and existing sessions stay unchanged.

The service returns display codes only after commit. It does not perform
cleanup afterward, so a cleanup error cannot hide the only copy of the new
codes after invalidating the old set.

Signed-in members and administrators can call `POST /api/v0/totp/backup-codes`
with `code`. HTTP 200 returns `data.backupCodes`, exactly ten strings, without
changing session cookies. The response is marked `Cache-Control: no-store`.
Missing or pending TOTP returns HTTP 409 (`totp-not-on`). Invalid or reused
codes return HTTP 422 (`invalid-totp-code`) with field location `body.code`;
shared throttling returns HTTP 429 (`totp-throttled`). Failed requests return
no backup codes. Unexpected causes stay private behind the shared 500 error.
