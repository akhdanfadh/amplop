# TOTP rules and ownership

This package generates enrollment secrets and verifies time-based codes.
The enrollment service encrypts secrets before passing them to storage.
Starting enrollment creates a pending factor; restarting replaces only a
pending factor. PostgreSQL stores the encrypted factor and serializes
updates by locking its user. Signed-in users can start or restart enrollment
with `POST /api/v0/totp` and read status with `GET /api/v0/totp`. Starting
enrollment does not activate TOTP; code confirmation is not implemented yet.

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
- `session.Storage` will declare an update-function method for confirmation:
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
in one transaction. The code-checking endpoints are not implemented yet.

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
ID. The limiter receives no code or secret. Code-checking HTTP operations will
map this error to HTTP 429 with problem type `/api/v0/problems/totp-throttled`.
