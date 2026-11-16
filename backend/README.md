# inbok backend

The Go server: the `inbok` command (`serve`, `worker`, `migrate`,
`health`, `export`, `recover-admin`, `search rebuild`), its HTTP API, the built web app it
serves, its background work, and its database code. Running `make` in the repository root lists a
shortcut for most commands here.

## Run the server

`serve` needs PostgreSQL with its migrations applied; see Local services
in `CONTRIBUTING.md` and Migrations below. From the repository root, load
`.env` and start the
development server:

```sh
cd backend
set -a; . ../.env; set +a
go run ./cmd/inbok serve
```

Or, to rebuild and restart the server whenever a Go file changes, use
[Air](https://github.com/air-verse/air). Its version is pinned in
`backend/tools.mod`, separate from the server's own dependencies in
`go.mod`:

```sh
cd backend
go tool -modfile=tools.mod air
```

Edits to `_test.go` files do not trigger a restart. The first run downloads
Air's dependencies. Air passes its shell's environment to `serve`, so load
`.env` there too.

In another terminal, check process health:

```sh
curl --fail-with-body http://127.0.0.1:8080/api/v0/health
```

Expect HTTP 200 with:

```json
{"data": {"status": "ok"}}
```

`inbok health` makes the same request to the address `serve` listens on
(`INBOK_HTTP_ADDR`; an all-interfaces address such as `0.0.0.0:8080`
reaches this machine), with a 3-second limit and no proxy. It exits 0 if the
answer is HTTP 200 and 1 otherwise, printing the reason. It reads no other
setting, so it needs no secrets. The container image runs it as its health
check, since the image has no `curl`.

## Background work

Work that outlasts a request, or must happen even if a process stops
halfway, runs in a separate process, `inbok worker`, on
[DBOS](https://docs.dbos.dev). DBOS saves each piece of work as a
**workflow** in PostgreSQL, in the `dbos` schema, and records each
finished **step**, so a stopped workflow resumes where it left off.
`serve` runs no workflows; it saves one with the change that needs it,
e.g. a [password reset link](#password-reset-link) with its request,
through a DBOS client (`dbos.Client`), so it too needs the workflow
tables `migrate` creates. ADR004 in the project's decision records
explains the choice.

`make dev` runs the worker beside `serve`, restarting it when a Go file
changes; `make dev-worker` runs only the worker, and `make backend-worker`
runs it once without restarts. The worker needs the workflow tables that
`migrate` creates; on a database that lacks them, or has them at an older
version than its DBOS needs, it stops with an error saying the system
database is at a lower schema version, and `migrate` fixes it.

The worker reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL, as for `serve`. |
| `INBOK_HTTP_ADDR` | `127.0.0.1:8081`; the image sets `0.0.0.0:8080` | Address the worker answers health checks on: port 8081 for the worker, 8080 for `serve`, so both run on one machine. `.env` sets `serve`'s, so the Make commands pass the worker `INBOK_WORKER_HTTP_ADDR` instead; `deploy/compose.yaml` sets `127.0.0.1:8081`. |
| `INBOK_WORKER_ID` | `worker` | The name the worker resumes unfinished work under: 1 to 64 letters, digits, `.`, `-`, or `_`. |
| `INBOK_PUBLIC_URL` | none, required | As for `serve`: links in the email the worker sends start with it, and its host names inbok to mail servers. |
| `INBOK_ENCRYPTION_KEYS` | none, required | As for `serve`: they decrypt the saved system email password and the refresh tokens of Gmail accounts, connected or removed. |
| `INBOK_GOOGLE_CLIENT_ID`, `INBOK_GOOGLE_CLIENT_SECRET` | empty | As for `serve`: the client the [account removal](#account-removal) job revokes tokens with, the [send](#sending-a-message) job sends with, and the [syncs](#synchronization) read Gmail with. Empty: the worker runs none of them and leaves them queued. |
| `INBOK_GOOGLE_BASE_URL` | empty, Google itself | As for `serve`: points that client at the [Google emulator](#google-emulator). |
| `INBOK_MEILISEARCH_URL` | none, required | Where Meilisearch answers: `http` or `https`, a host, and an optional port, e.g. `http://127.0.0.1:7700`. Serve [searches](#searching) it; the worker applies the [search index](#search-index)'s settings when it starts and keeps the index in step. |
| `INBOK_MEILISEARCH_KEY` | none, required | Meilisearch's master key, which signs every request. |

DBOS's own `DBOS__APPVERSION` and `DBOS__VMID` would replace the version
and the ID that decide which work a worker resumes (see below), so the
worker refuses to start while either is set.

The worker runs in UTC whatever the machine's time zone, so its log times
are UTC. DBOS names each scheduled run after its time with the local
offset, and workers in different zones would otherwise name, and run, one
hour's run twice.

The worker serves HTTP only for its health check. Once DBOS has started
and resumed the worker's unfinished workflows, it answers every request on
`INBOK_HTTP_ADDR` with HTTP 200 and no body, including the
`/api/v0/health` that `inbok health` asks for. That says the worker started, not that it is
still making progress: DBOS exposes no state of its queue loop to check.
`inbok health`, run with the worker's `INBOK_HTTP_ADDR`, checks it like
the server:

```sh
INBOK_HTTP_ADDR=127.0.0.1:8081 go run ./cmd/inbok health
```

Ctrl+C or SIGTERM closes the health listener, then shuts DBOS down: it
cancels the running workflows and waits up to 3 seconds for each stage of
its shutdown, and the worker exits after 7 seconds at most. A workflow
cancelled this way is not marked failed: it stays pending. So does one
whose worker was killed or lost power. Only DBOS's own shutdown leaves a
workflow pending; DBOS marks one cancelled any other way as `CANCELLED`,
which no worker resumes, so the worker never hands DBOS a context that a
signal cancels.

### Which worker resumes what

Work waiting in a queue belongs to no worker: every worker polls the same
queue tables, and each workflow goes to exactly one of them. Scheduled
runs are shared the same way, since each run's workflow ID comes from the
schedule's name and time. Once a worker starts a workflow, though, the
workflow belongs to that worker's `INBOK_WORKER_ID` until it finishes.

When a worker starts, it puts every pending workflow of its own ID back
on the queue, and they resume from the first step that had not finished.
So a step can run twice when a worker stops during it. DBOS does not check
whether another worker with that ID is still running it, and nothing hands
a workflow to a worker with another ID. Therefore:

- Keep the ID when restarting or replacing a worker.
- Never run two workers with one ID: each start would rerun the other's
  running workflows, side effects and all, e.g. a second email.
- A worker removed for good leaves its started workflows pending. Start a
  worker once with its ID, e.g. `INBOK_WORKER_ID=worker-2`, to finish
  them; DBOS's `ResumeWorkflow` also queues one for any worker, but only
  once the old worker is surely gone.

### Scaling

Run one worker and raise how many workflows it runs at once. A worker runs
each workflow in its own goroutine, so more worker processes on the same
machine do no more than more concurrency in one. A second worker is worth
it for another machine, or to keep one kind of work apart from another,
and it needs its own stable ID. Concurrency is a DBOS queue setting, kept
in the database and re-read by workers about every second, so `serve`
could change it at runtime, e.g. from an admin setting. No queue needs
that yet; the first job that does adds the setting. A worker starting
keeps a queue's saved settings rather than resetting them, so a change
to a queue's options in code needs its own update.

DBOS records a version on every workflow and resumes only workflows of its
own version. inbok fixes that version at `1` instead of DBOS's default, a
hash of the binary, so a new release resumes what the previous one left
unfinished. A workflow whose steps change must therefore still finish the
runs an older release started, e.g. with
[`dbos.Patch`](https://docs.dbos.dev/golang/tutorials/upgrading-workflows).

### Scheduled cleanup

Every hour on the hour (UTC), the workflow `inbok.cleanup`, scheduled as
`inbok.cleanup.hourly`, deletes rows nobody can use any more: email
verification, registration, and password reset links that expired at
least an hour ago, password reset attempts that no limit has counted
for at least an hour, both of their times being over 25 hours old, and
[Gmail connect states](#connecting) that expired at least an hour ago. The
hour covers a worker clock running ahead of the API's, which checks
expiry and limits with its own clock. Registration and password reset
requests no longer delete these rows themselves; a user's own expired
verification links still go when they ask for a new one. For each kind
it deletes up to 1,000 rows per step until a step finds fewer, and stops
after 100 steps; anything left waits for the next hour. Rows a request is
using are skipped, not waited for. A run that fails for one kind still
cleans the others, then ends in error; the next hour tries again. It does
not make up for hours the worker was down. Deleting is tidying, not
protection: redeeming refuses an expired link whether or not its row is
still there. While the worker is down, expired rows, which hold a token
hash and an email address, simply stay longer. The same run also deletes
old workflow runs, as [Inspecting workflows](#inspecting-workflows)
describes, and last sweeps the [message content store](#message-content-store)
of blobs no message part names that were written over a day ago, up to
1,000 a step: what a save that failed between storing content and saving
its rows left behind, and what a [content deletion](#content-deletion)
that gave up left. Each sweep step lists the store from the start, so
a large store costs a listing an hour. Each run logs `deleted expired
rows` with a count when it deleted any, and records the counts as its
output.

### Recovery notice

The workflow `inbok.recovery-notice`, run from the queue `inbok.email`,
tells every administrator by email that host recovery replaced an
administrator's password; see [Administrator recovery](#administrator-recovery).
Someone with the server can run recovery, so the other administrators
learn of it even if the recovered one's mailbox is no longer theirs.
`recover-admin` saves it in the recovery's own transaction, through a
DBOS client (`dbos.Client`) that runs no workflows, so a worker that is
down sends it once it starts. Its input is the recovered administrator's
ID and the time of the recovery, which the emails state. Its steps:

1. `list administrators` lists the IDs of the active administrators.
   DBOS records the list, so a resumed run sends to the same people. A
   failure is retried 10 more times over about 5 minutes, waiting 1
   second, doubling up to 1 minute; after that the run fails and sends
   nothing.
2. `send notice`, once per administrator, the recovered one first. In
   one query it reads the recipient's current address and whether they
   are still active, since a retry or a resumed run can come long after
   the list, and the recovered account's email. It sends with the system
   email settings: the recovered administrator gets the steps to
   finish recovery, the others an alert naming the recovered account. It
   ends without sending when the recipient is now missing or deactivated,
   or the settings are missing or have not passed a test, and gives up at
   once when the saved password no longer decrypts, the address is
   invalid, or the mail server refuses the recipient with a
   [permanent](#system-email) 5xx reply, so one dead address does not
   hold up the next administrators. Any other failure is retried 5 more
   times, waiting 1, 2, 4, 8, then 15 minutes. DBOS varies each wait by
   up to 5% and counts retries in memory, so a worker restarted
   meanwhile starts the count again.

A send that runs out of retries does not stop the others; the run fails
after the last one, with their errors. The sends run one after another,
so a mail server that stays down delays each by about half an hour.

The notice catches recovery run by someone careless, not by a careful
attacker: whoever can run recovery can also write to the database, and
so delete the queued run, stop the worker, or clear the system email's
tested time, which ends the sends still waiting.

An [unconfirmed](#system-email) send is retried too, so an administrator
may get the email twice: a second copy costs less than none. So does a
send that succeeded just before its worker stopped. The worker waits up
to 5 minutes for a mail server, not the 8 seconds a request allows,
which makes unconfirmed sends rarer. Each send logs `recovery notice`
with the `recovered_id`, the recipient's `user_id`, and its outcome, a
warning unless it was sent, or `recovery notice failed` with the error.
That error, which DBOS also keeps with the run, names only the failed
step, e.g. `failed at auth, unconfirmed`, never the server's reply: a
reply can quote what inbok sent, the SMTP password included. The
mailer's own warning has the reply, except a sign-in's, of which it keeps
only the code. DBOS's tables keep only IDs and outcomes with the run, no
address.

### Password reset link

The workflow `inbok.password-reset-link`, run from the queue
`inbok.email`, sends the link a [password reset request](#asking-for-a-link-1)
asked for. `serve` saves it in the request's own transaction, so a request
that was answered always gets its job, even if `serve` stops right after.
Its input is the request's attempt ID and the ID of the account with the
requested email, 0 for none; it holds no email. Its one step, `send link`,
locks the account and sends nothing when there is none, it is unverified
or deactivated, or it is over its limits; otherwise it saves a link and
mails it.

Saving and sending are one step because the link's secret must not be
recorded for a later one. A send that retrying cannot fix ends the run
with its outcome: system email not configured, an unusable address or
saved password, or a recipient the mail server refuses with a
[permanent](#system-email) 5xx reply. Any other failure,
[unconfirmed](#system-email) sends included, is retried 3 more times,
waiting 1, 2, then 2 minutes: the member is waiting on the page, and after
that asking again does better than a late link. DBOS counts these retries
in memory, so a worker restarted meanwhile starts the count again. Each
try saves a new link, so a retry may deliver a second working link, and
each counts toward the account's limits: the request's attempt counts the
first, and every later one gets an attempt row of its own with no client,
so the client's limit, which counts requests, leaves it out. A retry at
the account's limit therefore sends nothing.

The run logs `password reset link` with the `attempt_id`, the `user_id`,
and its outcome, a warning unless it was sent or skipped for lack of an
eligible account, or
`password reset link failed` with the error once its retries run out. That
error, which DBOS also keeps with the run, names only where the send
failed, e.g. `failed at timeout, unconfirmed`, never the server's reply,
which can quote the message and its live link.

### Password changed notice

The workflow `inbok.password-changed-notice`, run from the queue
`inbok.email`, tells a user that a reset link changed their password, so a
reset they did not ask for does not go unnoticed. [Completing a
reset](#completing-a-reset) saves it in the reset's own transaction, so a
reset that committed always gets its notice. Its input is the user's ID
and the time of the change, which the email names. Its one step, `send
notice`, reads the user's current email, since it may run long after the
reset, and sends nothing when the user is gone or deactivated. It reads
under a share lock, so it waits for a deactivation in progress.

A send that retrying cannot fix ends the run with its outcome, as for the
[reset link](#password-reset-link). Any other failure, unconfirmed sends
included, is retried 5 more times, waiting 1, 2, 4, 8, then 15 minutes,
like the [recovery notice](#recovery-notice): no one waits for it, and a
late notice still tells. So a user may get the notice twice. The run logs `password changed notice` with the
`user_id` and its outcome, a warning unless it was sent or skipped for a
user gone or deactivated, or `password changed notice failed` with an
error that names only where the send failed.

### Account removal

The workflow `inbok.account-removal`, run from the queue `inbok.google`,
finishes what removing a Gmail account leaves to do once its row is gone:
so far, asking Google to end inbok's permission. A
[disconnect](#disconnecting) deletes the account, moves its sealed refresh
token to an `account_removals` row, and saves the job, in one transaction,
so the job's input is only the removal's ID and the token never sits in
DBOS's tables. The same transaction saves the deletion of the account's
stored content, a job of its own (see [Content deletion](#content-deletion)),
so a content store that fails never holds the revocation.
Google work in the background runs on `inbok.google`;
members' sends have a queue of their own, so they never wait behind it
(see [Sending a message](#sending-a-message)).

Its step, `revoke token` (`connectedaccount.Removals.Revoke`), first
checks whether another member has connected the same Google account: a
revocation ends the Google account's whole grant to inbok's Google
project, theirs included, so it is then left alone (`shared`). Otherwise it
opens the token, sends it to Google's revocation endpoint in the request
body, and deletes the removal (`revoked`). Google answers a token it no
longer knows, e.g. one already revoked, with 400 `invalid_token`, which
counts as revoked, so a step that runs twice is harmless. A token no key
opens is dropped (`unreadable`). Any other failure is retried 5 more
times, waiting 1, 2, 4, 8, then 15 minutes; after that, `discard token`
deletes the removal unrevoked, so no sealed token is kept, and the token
lapses unused. The run logs `account removal` with the `removal_id` and
its outcome, or `account removal failed` with an error holding only
Google's status and OAuth error code.

The worker registers the workflow, and listens to `inbok.google`, only
when `INBOK_GOOGLE_CLIENT_ID` and `INBOK_GOOGLE_CLIENT_SECRET` are set.
DBOS would otherwise claim a removal it cannot run for that worker, and
no other worker could take it. Removals saved while the settings were set
wait in the queue, with their sealed tokens, until a worker that has them
runs; one that never does leaves them there.

### Content deletion

The workflow `inbok.content-deletion`, run from the queue `inbok.content`,
deletes the stored content of messages synchronization deleted. A sync
write that deletes messages with stored content saves the job in its own
transaction (`UpdateSyncedMail`), with the account's ID and the blob IDs
the deleted parts named. Saving it only adds a row to DBOS's queue, which
no worker sees until the transaction commits; a rollback removes it with
the rest. So the blobs go only once the rows that named them are gone.
Its one step, `delete content`, deletes them from the
[message content store](#message-content-store), which takes a blob
already gone as deleted; it is retried 5 more times, waiting 1, 2, 4, 8,
then 15 minutes. A run that gives up logs `content deletion failed` and
fails; the hourly sweep deletes those blobs a day later.

The workflow `inbok.account-content-deletion`, on the same queue, deletes
a disconnected account's whole key prefix. A [disconnect](#disconnecting)
saves it, with the account's ID, in the transaction that saves the
[account removal](#account-removal); it is a job of its own so that
neither waits on the other's service, and so a worker without Google
settings still runs it. Its one step, `delete account content`, is
retried the same way; a run that gives up logs `account content deletion
failed`, and the sweep deletes the blobs a day later, since the account's
part rows went with it.

The worker saves these jobs from its own steps, so it opens a DBOS client
for its storage, as `serve` does: DBOS refuses a workflow's own context in
a caller's transaction. Every worker runs the workflow and listens to
`inbok.content`.

### Sending a message

The workflow `inbok.outgoing-message`, run from the queue
`inbok.google-send`, sends a member's [outgoing message](#sending-mail).
Saving the message saves the job in the same transaction, under the
workflow ID `inbok.outgoing-message/<message id>`, so a message has one
send; its input is only the message's ID. Sends have their own queue: a
run keeps its place in a queue's limit until it ends, and priority only
orders runs still waiting, so on `inbok.google` a send could wait hours
behind long jobs once that queue has a limit. Neither queue has one yet.

Gmail's `messages.send` has no idempotency key, so the message's status
records how far sending got, and each step reads it first; a step run
again after a crash does only what is left:

1. `prepare send` (`outgoingmessage.Sending.Prepare`) takes an access
   token and reads `users.getProfile`, noting the mailbox's history ID for
   a later check. A token Google refused, one no key opens, or a
   deactivated member ends the message `failed` with `needs-reconnect`,
   `token-unreadable`, or `deactivated`.
2. `send message` takes a token and reads `users.getProfile` again: when
   Gmail names the account by another address than the message's `From`,
   e.g. after Google renamed it, nothing is sent: the message fails with
   `sender-changed` and the account takes Gmail's address, so the member's
   next message shows it. Every save of a step locks the member, then the
   account, then the message: a disconnect deletes the account and then
   its messages, so saving the address after locking the message would
   deadlock with it. The check is in this step because DBOS reuses an
   earlier step's result after a restart, however old. The step then marks
   the message `sending`, reading the member's deactivation again under
   their share lock, and calls `messages.send`, with the `threadId` a reply
   was saved with. From that call on, a stop,
   e.g. the worker shutting down, cancels neither the request, which keeps
   its 30-second deadline, nor saving its answer: cutting it off would turn
   an answered send, or one that never left, into `unknown`. Gmail's IDs end
   it `sent`.
   An answer that says the message was not sent ends it `failed`:
   `refused`, or `sending-limit` for Gmail's daily limit, a 429 whose text
   names mail sending. A failure before any connection to Gmail, another
   rate limit, or a refused access token puts it back to `waiting` and is
   retried; a limit that outlasts the retries, e.g. Gmail's daily bandwidth
   limit, ends it `gave-up`. Anything else, e.g. a dropped connection, a
   timeout, a 5xx, or a 200 without an ID, is unanswered: the message stays
   `sending`. Only no connection proves Gmail never had the request, since
   Go reports a finished write after the fact. A step that finds the
   message `sending` never sends again. Gmail, and the emulator, put a sent
   message in Sent themselves.
3. After an unanswered send, the run waits 2 minutes, then `check sent`
   lists the messages Gmail added to Sent since the noted history ID
   (`history.list`, up to 10 pages) and reads each one's
   `X-Inbok-Message-Id`. Gmail replaces the `Message-ID` a client sets but
   keeps that header. A match ends the message `sent`, with Gmail's own
   time for it (`internalDate`), not the check's, which runs minutes later,
   as the member's mailbox shows it. No match, a message deleted before it
   could be read, history Gmail no longer keeps, or no token ends it
   `unknown`: it is never sent again, and the member decides.
4. A step that keeps failing ends with `give up`: a `waiting` message
   fails with `gave-up`, and a `sending` one, which may have gone, becomes
   `unknown`. A `give up` that keeps failing too, e.g. with the database
   down, is tried again every 15 minutes until it is saved, since a run
   that ends with an error is not resumed.

`prepare send` and `send message` are retried 5 more times, waiting 2, 4,
8, 16, then 30 seconds, since the member is waiting; `check sent` 3 more
times, waiting 30 seconds, 1, then 2 minutes. The run logs `outgoing
message` with the `message_id` and the status it ended with, a warning
unless `sent`, or `outgoing message step failed` with an error that holds
only Gmail's status. The Gmail client is given a logger that drops
everything: Google's own would write each request's body, the whole
message, to stderr with `GOOGLE_SDK_GO_LOGGING_LEVEL=debug`. ADR006
records the rules and what they were chosen over. The worker registers the
workflow, and listens to `inbok.google-send`, only when the Google
settings are set, as for account removal.

### Synchronization

Every sync of a connected account's mail is the workflow `inbok.sync`, run
from the queue `inbok.google`, as [Synchronized mail](#synchronized-mail)
describes. Its input is only the account's ID. DBOS refuses a second sync
of an account while one is queued or running (its deduplication ID,
`sync-<account ID>`), so a poll and a full sync of one account never run
at once. [Connecting](#connecting) an account saves its sync state,
`waiting`, and its first sync, in the transaction that saves the account.
The enqueue runs in a savepoint (`dbos.Client.EnqueueSync`): DBOS refuses
its return-existing policy inside a caller's transaction, and its
duplicate would abort the whole transaction, so a sync already queued or
running rolls back only the savepoint and keeps its place.

Its first step, `plan`, reads the account's sync state and records which
sync to run, so a resumed run makes the same choice: a `complete` account
polls, and any other runs a full sync, which polls between its pages too. A poll that Gmail answers 404, for
history it no longer keeps, goes on into a full sync in the same run. The
run's output names the kind it ran, `poll` or `full`, and the status it
left. Each step that calls Gmail takes its own access token
([Calling Gmail](#calling-gmail)), so none is ever saved, and one that
writes only inbok's data asks `Access.Check`, which reads the same state
without calling Google.

**The schedule.** The workflow `inbok.sync-schedule` runs at the start of
every minute, in UTC (the schedule `inbok.sync.minutely`), and does not
make up for minutes the worker was down. `mailsync.Schedule.Due` picks the
accounts whose member is active and which need no reconnect, then:

- a `waiting` account, and one `failed` an hour ago or longer, starts a
  sync, so a reconnect, a reactivation, or a passing outage needs no code
  of its own;
- a `running` account starts one too: DBOS gives back the run that still
  lives, and one that ended without recording how, e.g. when the database
  stayed down past the last step's retries, gets a new full sync rather
  than staying `running` for good;
- a `complete` account starts one when Gmail's current history ID
  (`users.getProfile`, 1 unit) differs from its stored position, or when
  a failure is recorded, which the poll then clears. The checks run ten
  at a time, 20 seconds at most each and 40 seconds in all, so runs never
  overlap; each minute starts at another account, and those not reached
  wait for the next minute. A check that fails starts a sync anyway, whose
  retries decide the failure, unless a failure is already recorded; one a
  stopped account refuses starts nothing. A check that finds the position
  unchanged on an account with no failure records `last_synced_at` as the
  time Gmail answered, in one statement after the checks
  (`ConfirmSynced`), only where the account is still `complete` at that
  position and only forward, so a poll that finished meanwhile keeps its
  own. `last_synced_at` so means when inbok last confirmed its copy, and a
  time minutes old on a `complete` account means syncing stopped.

The run enqueues `inbok.sync` for each, keeping a sync already queued or
running. An idle account then costs one run a minute in all, the
schedule's, and 1 unit of its Gmail quota;
[message bodies](#message-bodies) have a minute's schedule of their own.

**Sync now.** `POST /api/v0/connected-accounts/{id}/sync`
(`mailsync.Service.Request`) enqueues `inbok.sync` for one of the member's
accounts at once, skipping the minute's wait, the check, and the hour
after a failure; the plan still chooses poll or full sync. One transaction
locks the sync state, refuses another member's account and one that needs
reconnecting, allows one request per account every 15 seconds, records
`sync_requested_at`, and enqueues as above. It locks the account's row
(`FOR SHARE`) before the sync state, the order a disconnect takes, so the
reconnect state it read holds until it commits. The limit is there because
a poll that finds nothing ends in about a second, so a script could
otherwise leave a run in DBOS's tables every second. `plan` records the
`sync_requested_at` it read, and a poll that succeeds reads it again (the
step `recheck`): when it changed, a request came since the poll's reads
began, so it polls again from the new position. The stored values are
compared, not times, since the server writes the request and the worker's
clock may differ. A request so starts a read of Gmail after it, and its
mail shows within seconds. A request that lands after that last look,
while the run ends, finds the run and starts none; the next minute's check
then starts its poll, as without the request, unless the account has a
failure recorded and its checks keep failing, when it waits for a check
that succeeds or, after a failed full sync, the hour's retry (ADR001
accepts this: Gmail is failing then anyway). The page does not track when
a request is answered: that would take exact bookkeeping across the two
processes' clocks.

**A poll** reads each page of `history.list` since the stored position
once, as the step `read changes` (`mailsync.Poll.ReadChanges`), which
records the page's IDs and label IDs, no mail. Then `apply changes`
(`Poll.ApplyChanges`) applies the page in parts: the deletes, then where
each updated message stands, its whole label set as its mailboxes and
flags, 1,000 at a time; then the messages added, fetched and saved 100 at
a time; then those whose record named no labels, fetched 100 at a time to
learn their state. A page naming thousands of messages, e.g. a bulk
import, is so applied in short steps, and a stopped worker resumes at the
part it reached. An update to a message the copy does not hold is
skipped: only mail older than the account's first window is not held.
The first part of the first page also lists the labels, whose creation,
renaming, or deletion history does not report; a recorded label the
listing no longer has was deleted since, and is left out. Only the last
part of the last page moves the position, records the time, and clears a
failure, so a poll stopped for good starts over from the stored position,
and each part writes only while the account is still `complete` at that
position. A poll never changes the status the member
sees: a stopped account or a newer sync ends it with nothing recorded,
and a failure records `failure` and `failed_at` beside `complete`.

**A full sync** copies the last 90 days of the account's mail metadata and
mailboxes, by Gmail's own received time, or back to the oldest message the
copy holds if that is older: a full sync run again after a 404 then
brings every stored message current, whatever its age. Mail that aged out
of the window stays until Gmail deletes it. Its steps are
`mailsync.FullSync`'s methods, in order:

1. `start` lists the mailboxes and reads the account's position
   (`users.getProfile`) before listing any message, so the first poll
   replays what changed while the full sync ran. It saves both with a new
   sync generation, `running`.
2. `copy page`, once per page of 100 messages, newest first: it lists the
   page, Spam and Trash included, fetches the messages' metadata, and
   saves the ones received inside the window. Gmail's `after:` counts
   whole seconds, so the step checks each message's own time at the
   window's edge. A message gone before its fetch is deleted. A message in
   a label made since `start` lists the labels again, and one in a label
   deleted since is saved without it. Every 4 pages the run polls, with the
   poll's steps scoped to the running full sync, so mail that arrives
   during a long copy appears within about two minutes. Such a poll writes
   while the account runs at the run's generation, its saves are kept by
   the sweep, and its last part moves only the position; a failure fails
   the full sync, and a 404 stops these polls.
3. `delete earlier` deletes, up to 1,000 at a time, the account's messages
   this generation did not save: the ones Gmail no longer has.
4. `finish` records `complete`, the time, and no failure.

A running full sync's progress is not stored: the list counts the
account's messages stamped with its generation, through the account's
message index, which a repeated page or a poll during the copy counts
once each.

Every full sync step writes only while the account's sync state still has
the step's generation and is `running`, so a full sync another one took
over stops at its next step. A deactivation or a refused token that lands
while a page's metadata is fetched does not stop that page's save: the
page was read while the account could sync, and the next step stops. How
a step's error ends the sync is `mailsync.EndingOf`:

| Error | Retried | Then, for a full sync | Then, for a poll |
|---|---|---|---|
| The account needs reconnecting, or its member is deactivated | no | `wait`: back to `waiting`, until the member reconnects or is reactivated | nothing to record |
| The account was disconnected, or another sync took over | no | nothing to record | nothing to record |
| Gmail no longer keeps the history asked for (404) | no | | a full sync, in the same run |
| The refresh token no key opens | no | `fail`: `failed`, `token-unreadable` | `poll fail`: `token-unreadable` |
| Gmail failing, e.g. `mailsync.ErrProviderBusy`, or Google's token endpoint failing (`connectedaccount.ErrRefreshFailed`) | 4 more times, within the step, waiting 30 seconds, then 1, 2, and 2 minutes | `fail`: `failed`, `provider` | `poll fail`: `provider` |
| Anything else, e.g. the database, also while taking a token | the same | `fail`: `failed`, `internal` | `poll fail`: `internal` |

Gmail's 401 for an access token it no longer accepts is retried like
Gmail failing. The `google` client has already dropped the token from
`Access` (below), so the retry trades the refresh token again: a grant
the member revoked then ends as needing a reconnect.

A failure records its time, `failed_at`, and fails the run, so DBOS keeps
it 30 days. The run logs `sync step failed` with the step and an error
holding only Gmail's status when a step runs out of retries, then
`full sync failed` or `poll failed` with the failure, or `full sync
ended`, `poll ended`, or `sync ended` with how it ended otherwise; each
line carries the `account_id`. A step retries within itself and returns
how it ended as a value, which DBOS records whole: DBOS records a step's
error only as text, so a worker resumed after it would no longer know a
Gmail failure from inbok's.

A sync stopped with its worker resumes at the step it stopped in (see
[Which worker resumes what](#which-worker-resumes-what)): updating the
containers copies nothing twice, and polls go on from the stored
position. The worker registers both workflows and the schedule, and
listens to `inbok.google`, only with the Google settings, as for the
[account removal](#account-removal); without them it deletes the schedule
an earlier start saved.

### Message bodies

The workflow `inbok.message-bodies`, run from the queue `inbok.google`,
fetches the content of recent mail in the background, so a member opens
it without waiting and search finds it by its text. Its input is only the
account's ID: the account's own data lists what to fetch. A message needs
its body while it has no part rows, Gmail received it in the last 90 days,
it is in no Spam or Trash mailbox (ADR001 keeps those metadata only), and
its fetch has not failed for good in the last day (`body_failed_at`).

The workflow `inbok.message-bodies-schedule` runs at the start of every
minute, in UTC (the schedule `inbok.message-bodies.minutely`), and its
step `pick due bodies` (`messagecontent.Fetcher.DueAccounts`) makes one
query for the accounts whose member is active, which need no reconnect,
whose full sync is `complete`, and which have a message needing its body.
The rule's policy is Go's: the window (`mailsync.Window`), the day's
skip, and the mailboxes left out (`mailsync.MetadataOnlyRoles`), which
the queries take as parameters.
For each, it enqueues `inbok.message-bodies`, keeping one already queued
or running (its deduplication ID, `message-bodies-<account ID>`); its
output is how many it asked for. Bodies so wait for an account's first
copy, which with the whole pace fills the mail list up to four times as
fast. A later full sync, after Gmail dropped the account's history, can
start while a body run is going; the run keeps on, sharing the pace
(ADR009, "Fetching content", accepts it). The accounts are found from their data
each minute, not from events: a quiet account, which gets no sync run,
still has a run that ended at its limit or failed, or a message whose day
passed, picked up within a minute. A pick that fails fails the run, and
an enqueue that fails is skipped; they log `message body check failed`
or `message body fetch not started`, and the next minute tries again.
The schedule is apart from the sync schedule, whose successful runs tell
members that syncing works (`syncStopped`, [Connected
accounts](#connected-accounts)), so body work that stalls, e.g. behind a
lock, never makes syncing look stopped.

Each step, `fetch bodies` (`Fetcher.FetchDue`), takes an access token and
the 20 newest messages that need their body, asked again each step, so
mail arriving during a long fetch goes first. It fetches them one at a
time with `messages.get` in full format (60 units, on the account's pace
that sync's fetches share: about 80 messages a minute), reads each part
tree ([Message parts](#message-parts)), fetches a text part Gmail sends
by `attachmentId` on its own, and saves the message as the
[message content store](#message-content-store) describes. A message
Gmail no longer has (404 or 410), whose answer passes 16 MiB, which
Gmail refuses (a 4xx other than 401, 403, 408, or 429), whose answer
does not decode or has no value, or with over 1,000 parts records
`body_failed_at`, logs `message body fetch failed` with its `message_id`,
and the step goes on; one synchronization deleted meanwhile is passed
over. Any other error, e.g. Gmail's rate limits or a 5xx past their
retries, a network failure, or the store or database down, concerns the
whole account: it ends the run, logged as `message body fetch failed`
with the `account_id`, without retries, and the next minute starts
another. A per-message problem that shows only as such an error, e.g. a
5xx one message alone causes, would end every run at that message; inbok
accepts that unlikely case. An account that needs a reconnect, has a
deactivated member, is gone, or has a token no key opens ends the run
quietly, as the sync records why.

A run takes at most 50 steps, about 1,000 messages: some 12 minutes when
each message's text comes in its first answer, longer when text comes
separately or calls are retried. It so stays short to resume, and holds
its place in `inbok.google`'s limit, if one is set, for a bounded number
of messages; the next minute's schedule goes on. A mailbox with 10,000
messages in 90 days so has its list after about 42 minutes and its bodies
about 2 hours later.

The worker registers both workflows and the schedule only with the Google
settings, as for the [account removal](#account-removal); without them it
deletes the schedule an earlier start saved.

### Inspecting workflows

DBOS keeps each run in `dbos.workflow_status` and each finished step's
result in `dbos.operation_outputs`. The hourly cleanup deletes a run, with
its steps, 7 days after it succeeded and 30 days after it failed: `ERROR`,
`CANCELLED`, or `MAX_RECOVERY_ATTEMPTS_EXCEEDED`. Nearly every run
succeeds and is rarely looked at again, while a failure may be looked into
days later. Runs that have not finished are never deleted, however old. In
`psql`, recent cleanup runs:

```sql
SELECT workflow_uuid, status, executor_id, to_timestamp(created_at / 1000) AS created
FROM dbos.workflow_status
WHERE name = 'inbok.cleanup'
ORDER BY created_at DESC
LIMIT 10;
```

`status` is `SUCCESS`, `ERROR`, `PENDING` (running, or waiting for its
worker to start again), `ENQUEUED`, `DELAYED` (enqueued with a delay that
has not passed), `CANCELLED`, or `MAX_RECOVERY_ATTEMPTS_EXCEEDED`. A run's
steps:

```sql
SELECT function_id, function_name,
       convert_from(decode(output, 'base64'), 'UTF8') AS output, error
FROM dbos.operation_outputs
WHERE workflow_uuid = '<workflow_uuid>'
ORDER BY function_id;
```

DBOS stores outputs as base64 of their JSON, hence the decode, e.g.
`{"Deleted":1000,"More":true}` for a cleanup step.

## Container image

From the repository root, `make image` builds the `inbok:local` image
from the `Dockerfile`; `IMAGE=name:tag` picks another name. Nothing is
pushed to a registry. It holds the binary, with the web app built in, on
distroless's `static` base: no shell, no package manager, and the
`nonroot` user (65532). It sets `GIN_MODE=release` and
`INBOK_HTTP_ADDR=0.0.0.0:8080`, since a container's loopback is
unreachable from other containers. Containers on the same Docker network
can then reach it, and the host's network only if the port is published.
To publish it for a proxy on the same host, bind loopback, e.g.
`-p 127.0.0.1:8080:8080`: a bare `-p 8080:8080` listens on every host
interface, and Docker's rules bypass host firewalls such as ufw. Its entry point is `inbok`, and its default command is
`serve`, so `docker run inbok:local migrate` runs a migration and
`docker run inbok:local worker` the worker.

The base images are pinned by version and digest; the `Dockerfile` says
how to update one. Builds cache downloaded Go modules and pnpm packages
between runs, and a change under `backend/` reuses the cached web build.
`deploy/README.md` runs the image with Docker Compose on a server.

## The web app

`serve` also serves the built web app on every path outside `/api`, so the
page and the API share one origin and one version. During development
Vite serves the page instead (`make dev`), and a `go build` or `go run`
from a fresh checkout includes no web app: `serve` logs
`this build has no web app; paths outside /api answer 404` and does
exactly that. To build one binary with the web app inside, from the
repository root:

```sh
make backend-build
```

It builds the web app, copies `web/dist/client` into
`backend/internal/webapp/dist`, and writes `backend/bin/inbok`. Go embeds
that folder when it compiles, so rebuild after any web change. Only the
folder's `.gitkeep` is committed. The copy stays after the build, so later
`go build`, `go run`, and Air also include it until you empty the folder:

```sh
find backend/internal/webapp/dist -mindepth 1 ! -path backend/internal/webapp/dist/.gitkeep -delete
```

For a GET or HEAD outside `/api`:

- A built file's exact path sends it, e.g. `/assets/index-BrBO-kCw.js`.
  Files under `/assets/` have hashed names and get
  `Cache-Control: public, max-age=31536000, immutable`; other files, and
  the shell, get `no-cache` and an `ETag` so the browser rechecks them.
  A `Range` header is ignored.
- Any other path gets `_shell.html` with 200, e.g. `/security`, and the
  browser renders the route.
- A path under `/assets/`, a path with a file extension, e.g.
  `/favicon.ico`, and a path with a segment starting with `.` get the
  API's Problem Details 404 (see Errors) instead: a browser that asks for
  a script never receives the page.

Other methods outside `/api` get a 405 with `Allow: GET, HEAD`, after the
same cross-origin check as the API: a write without `Origin` or
`Sec-Fetch-Site` gets its 403 first. `/api` and every path under `/api/`
belong to the API, so `/api/v1/x` is an API 404, not the shell. There are
no directory listings, and only the files in the build can be sent.

### Security headers

Every response from inbok's handlers, the API's and the web app's,
carries `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
and `X-Frame-Options: DENY`. The web app's files and shell also carry this
`Content-Security-Policy`:

```text
default-src 'self'; script-src 'self' <shell hashes>; object-src 'none';
base-uri 'none'; form-action 'self'; frame-ancestors 'none'
```

Pages load only inbok's own files and call only its own API. No `<style>`
element or `style` attribute in the HTML applies; styles set from script,
e.g. React's `style` props, still do. The build's shell has inline
scripts, and their text changes with every build, so `serve` reads the
embedded shell at startup and allows each one by its SHA-256 hash; no
other inline script runs. Nothing in the policy names a part of the web
app's code: whatever the build's shell holds is allowed, and the web app
keeps to the rest. A message body is the exception to `DENY`: the web
app frames it, so it sends `SAMEORIGIN` and a policy of its own (see
[Showing HTML](#showing-html)). Other API responses get no
Content-Security-Policy; Huma's docs page at `/api/v0/docs` sets its
own, since it loads scripts from unpkg.com. HTTPS-only (HSTS) is the
proxy's header, because Go itself serves plain HTTP. `web/README.md`
lists what the policy means for web code.

## API contract

GET, HEAD, and OPTIONS must not perform user actions, such as changing mail.
Session checks may refresh activity timestamps or delete expired session rows.
Cross-site protection always allows these methods; use write methods for actions.

Access checks use Huma middleware because it can read the matched operation's
`Metadata["access"]`. Declare `session.AccessPublic` for public operations,
`session.AccessMember` for any valid session, or `session.AccessAdmin` for
administrators with confirmed TOTP. Missing or invalid metadata requires admin
access. This metadata is shared endpoint configuration. Each request's context
holds the checked session reference and user's identity. Handlers pass the
reference to session operations without reading its hash.

Protected responses use `Cache-Control: no-store` so browser and proxy HTTP
caches do not retain account data or reuse stale authentication responses.
This does not prevent storing the session cookie or clear the web app's
TanStack Query cache.

Write requests must include `Sec-Fetch-Site` or `Origin`. Modern browsers
send these headers. For manual requests to the local API, send
`Origin: http://127.0.0.1:8080`. Proxies must preserve these headers and
the original `Host`. Missing source headers receive a 403.

The server uses Go's [CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection)
for Fetch Metadata and Origin checks, including setup and sign-in. It adds
the missing-header rejection above; Go alone permits requests without both
headers. There is no CSRF token or configured cross-origin exception. Keep the
web app and API on the same origin, including the scheme, host, and port.

A browser request also says what it loads the answer as, in
`Sec-Fetch-Dest`. `withFetchDest` answers only the web app's `fetch()`
calls (`empty`), and only a page load (`document`) for an operation
tagged Redirects; anything else, e.g. an `<img>` naming an API path, gets
a 403 `fetch-destination-refused` before the session check. An
operation a browser loads another way lists its destinations under the
`fetchDestKey` metadata, e.g. `iframe` for a message body. A request
without the header, e.g. from curl, passes. The message view frames
mail on inbok's origin, so an image in a message naming an API address,
which the sanitizer keeps only as an absolute URL once the member loads
remote content, would otherwise send that GET with the member's cookie;
a GET still changes nothing.

JSON success responses put their payload in `data`. Response schemas live
only in the OpenAPI document: responses carry no `$schema` field or `Link`
header. Operation IDs use camelCase. Each operation lists one tag from
`tags.go`, which groups it in the API reference, and a summary that is a
Title Case title of at most five words, e.g. `Sign In with Code`; what it
does in detail goes in `Description`. `TestTags` and
`TestOperationSummaries` check both. Handlers build success bodies with
`respondData`. Schema names come from Go type names, e.g.
`ResponseHealthStatus`, and become the generated client's type names, so
renaming a payload type is a breaking change.

Huma's operation metadata and Go response types define the API contract.
Each operation's response helper calls `problemResponses(api, statuses...)`,
then sets its failure descriptions and examples. Pass that map as `Responses`
when registering the operation. The shared helper includes a `default`
Problem response for shared errors. Avoid `Operation.Errors`: Huma adds
separate 422 and 500 entries to that list.
The API overview explains shared errors, shows a field-error example, and
lists every problem type in a table that `registerProblemTypes` builds from
the operations' examples and `sharedProblems`.

The API base path is `/api/v0`; operation paths such as `/health` are
relative to it. The OpenAPI document advertises this relative base, so it
works on whatever host serves it. Locally, that is `http://127.0.0.1:8080`.
View its documentation at `http://127.0.0.1:8080/api/v0/docs` and its OpenAPI
document at `http://127.0.0.1:8080/api/v0/openapi.json`.

The committed copy of that document is `api/generated/openapi.json`. After
changing an operation, regenerate it from `backend` without starting the
server:

```sh
go run ./cmd/inbok export ../api/generated/openapi.json
```

## The probe

The probe is an example database write, removed before the first release.
It adds `amount` to a shared counter and records the entry, both in one
transaction:

```sh
curl --fail-with-body -H 'Content-Type: application/json' \
  -H 'Origin: http://127.0.0.1:8080' \
  -d '{"note": "hello", "amount": 3, "highlighted": false}' \
  http://127.0.0.1:8080/api/v0/probe/events
```

Expect HTTP 201 with the saved event and the new total, e.g.
`{"data": {"event": {"id": 1, "note": "hello", ..., "tag": null}, "total": 3}}`.
A body that breaks the contract gets a 422 listing rejected inputs, e.g.
`{"location": "body.amount", "message": "Enter a valid value."}`. Past a
total of 1000 it answers 409 with type `/api/v0/problems/counter-full`
and detail `The counter is full.`; to empty it, run
`go tool -modfile=tools.mod goose reset`, then `migrate` again.

## Errors

Error responses use `application/problem+json`. They require `type`,
`status`, `title`, and `detail`. The body status equals the HTTP status;
the type identifies the condition, e.g. `/api/v0/problems/counter-full`.
These versioned paths work on each installation's own origin. `instance`
is initially omitted; tracing uses the `X-Request-ID` header.

Handlers map known service errors with `newProblem`, choosing safe public
text and a stable type. This includes expected 5xx failures: an explicitly
mapped 503 can explain maintenance. Unclassified errors are returned as is,
become a fixed 500 `internal-error`, and log their technical cause with the
request ID. One that the request's own deadline caused becomes a 503
`service-busy` instead (see [Timeouts and shutdown](#timeouts-and-shutdown)),
and one that is the cancellation of a request whose client left is
logged at info level as "client closed request", status 499, nginx's
code, which a client rarely receives, e.g. one that closed only its
sending side. The error must be that cancellation alone: joined with
another failure, it stays a 500.
Huma status helpers alone do not declare text safe for display.

The contract lists problem types in the `ProblemType` schema:
`registerProblemTypes` collects every type an operation shows in a response
example, plus the shared types the API overview's table covers. A type a
handler maps but no operation documents is missing from the list, so
document each mapped problem with an example. `Problem.type` is any of the
listed types or any other URI reference, so clients still accept a type the
list lacks.

Optional `errors` is a nonempty list of rejected inputs. Each entry has a
`location` and safe `message`, without `value`. Missing required fields have
precise locations, e.g. `body.items[3].name`, and "This field is required."
Numeric bounds, whole numbers, and string lengths receive correction text,
e.g. "Enter a whole number from 1 to 10." Simple fields with both limits get
a combined range; other fields keep the individual correction. Other Huma
input errors remain generic, because its original messages can include
rejected values. Explicit business mappings can also identify rejected inputs,
including on a 409. Public messages contain no raw technical causes.

Huma still validates request rules. `configureErrors` installs its error
model once before API construction, so runtime responses and the exported
OpenAPI schema describe the same public fields.

Huma reports a missing property's parent location. `configureValidation` uses
its formatter to carry the schema's field name in a private message marker.
`publicInputError` consumes that marker to build the precise location; the
marker never appears in public responses or OpenAPI. This relies on Huma's
required-property formatter arguments, covered by HTTP tests when upgrading.

For numeric and length limits, the formatter records safe text alongside
Huma's cached message. It uses schema limits, never submitted values. Responses
look up that text without parsing English messages; unknown messages keep the
generic fallback.

For known numeric and length failures, the response formatter follows the
operation's body schema to the rejected field. Ordinary object and array paths
can combine both limits, e.g. "Use 1 to 40 characters." Unsupported paths,
combined schemas, and extra numeric rules retain the individual correction.
Identical location/message pairs appear once, even when several rules fail.

Create all APIs and finish registering operations before serving requests.
Schema preparation populates the message lookup at startup; request handling
only reads it. Tests follow this order while sending requests concurrently.

Input failures retain their meanings: malformed requests use 400, body-read
timeouts 408, body limits 413, unsupported content types 415, and schema
violations 422. A 406 uses `not-acceptable`, title `Response format not
supported`, and detail `Ask for a JSON response.` The current configuration
falls back to JSON for unsupported `Accept` values; it does not enable strict
response-format rejection.

Unknown routes (404) and unsupported methods (405, with an `Allow` header)
return the same Problem Details, never Gin's plain-text bodies. This
includes the web app's missing files and its other methods; see The web
app. A trailing slash is a different route: `/api/v0/health/` is a 404,
not a redirect.

## Timeouts and shutdown

Each request receives a context with a 10-second deadline. Database queries and
other calls using that context can stop when it expires. The deadline does not
forcibly stop handlers or undo completed writes. Returning the context's
deadline error from a handler, wrapped or not, after the deadline produces a
503 `service-busy` with `Retry-After`, the deadline in whole seconds, and logs
the cause as a warning, "request timed out". The request's own log line is a
warning too, as for every 503; other 5xx lines are errors. A deadline error
from inside a call while the request still has time stays an unexpected 500. A
write may have committed before the deadline, so the 503 does not say it was
not. A client that leaves cancels its request's context; serve's shutdown
never does, as it waits for requests instead. Returning that cancellation
logs "client closed request" at info level, and the request's line shows
status 499, as a client leaving is no failure of inbok's; any other error
while the client leaves stays an unexpected 500.

Stop the server with Ctrl+C or SIGTERM. It stops accepting connections and gives
in-flight requests up to 5 seconds to finish. The password reset link and
the password changed notice are [background jobs](#background-work) saved
with their request, so stopping loses neither; other email is sent inside
its request.

## Configuration

`serve` reads these environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_HTTP_ADDR` | `127.0.0.1:8080`; the image sets `0.0.0.0:8080` | Address and port to listen on, e.g. `0.0.0.0:8080`. |
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL; see Migrations below. |
| `INBOK_PUBLIC_URL` | none, required | The address users open inbok at, e.g. `https://mail.example.com`; see Public URL below. Invalid values stop startup. |
| `INBOK_TRUSTED_PROXIES` | empty, trust none | Comma-separated proxy network ranges in CIDR notation. Invalid entries stop startup. |
| `INBOK_SETUP_TOKEN` | empty, setup unavailable | Operator-generated first-admin setup token. If nonempty, at least 32 Unicode code points. |
| `INBOK_ENCRYPTION_KEYS` | none, required | Keys that encrypt stored TOTP secrets, the system email password, and connected Gmail accounts' refresh tokens; see Encryption keys below. Invalid entries stop startup. |
| `INBOK_GOOGLE_CLIENT_ID` | empty, Gmail not offered | The Google OAuth client members connect Gmail accounts with. Set it and the secret together, or neither. |
| `INBOK_GOOGLE_CLIENT_SECRET` | empty, Gmail not offered | That client's secret. Surrounding spaces are removed from both. |
| `INBOK_GOOGLE_BASE_URL` | empty, Google itself | For development and tests only: one server on this machine that answers for Google, e.g. `http://127.0.0.1:4000`; see [Google emulator](#google-emulator). Any other host, or no client, stops startup. |
| `INBOK_S3_ENDPOINT` | none, required | The S3 service that holds message content, e.g. `http://127.0.0.1:8333`; see Message content store below. |
| `INBOK_S3_ACCESS_KEY_ID` | none, required | The key ID inbok signs in to it with. |
| `INBOK_S3_SECRET_ACCESS_KEY` | none, required | The secret for that key ID. |
| `INBOK_MEILISEARCH_URL` | none, required | Where Meilisearch answers, as the worker reads it; serve [searches](#searching) it. Serve starts without asking it, and a search answers 503 while it is down. |
| `INBOK_MEILISEARCH_KEY` | none, required | Meilisearch's master key, as the worker reads it. |
| `GIN_MODE` | `debug`; the image sets `release` | Read by Gin itself. Use `release` for a deployed server; `debug` prints routes at startup. |

`export` reads none of them, so the committed document is the same on every
machine.

### Message content store

Message content, such as the text of a message, lives in an S3-compatible
object store; PostgreSQL keeps what describes it. Every stack runs
[SeaweedFS](https://github.com/seaweedfs/seaweedfs) for it, one container
started with `weed mini`. Both `serve` and the worker read the three
`INBOK_S3_*` settings. The worker, which writes most content, creates the
bucket `inbok` at startup if it is missing, so a wrong address or key
stops it there, and a store that does not answer stops it within 10
seconds. `serve` starts without contacting the store: the store is a
cache, so while it is down, only requests that read content fail.

`INBOK_S3_ENDPOINT` is the origin alone: `http` or `https`, a host, and an
optional port. inbok adds the bucket and key to the path (path-style
addressing). Plain `http` suits a store on a private network, as in the
Compose files: requests are signed, so the secret never travels, but the
content does, unencrypted. The keys are taken as they are.

SeaweedFS makes the keys in its own `AWS_ACCESS_KEY_ID` and
`AWS_SECRET_ACCESS_KEY` its admin identity on every start, and the Compose
files pass inbok's keys there. To change them, change them in `.env` and
start SeaweedFS again; the old key stops working and the content stays.
SeaweedFS's S3 port checks the keys, but its other ports (master, filer,
volume, and their gRPC ports) let anyone who reaches them read and write,
so only the S3 port is published, to this machine only, and the deployment
keeps SeaweedFS on a network of its own with inbok and the worker.

What describes the content is in PostgreSQL's `message_parts`: one row
per MIME part of each message whose content was fetched, a message having
rows for all its parts or none. A row keeps the part's place in the tree
(`path`, the index of each part on the way down from the top part),
Gmail's `partId`, media type, charset, file name, size, disposition,
`Content-ID`, a `multipart/related` part's `start`, its kind (see
[Message parts](#message-parts)), and the blob ID its bytes are stored
under: at once for a text part, and for an attachment or inline part once
a member first asks for it. The blob ID is JMAP's `blobId` (RFC 8621,
section 4.1.4), not the part's `Content-ID`, which is `mime_content_id`.
Rows reference their message with the account,
deleted with it.

`messagecontent.Contents.Save` writes a message's content in the order
ADR009 sets, since the store and PostgreSQL share no transaction:

1. Store each text part's content under a new random blob ID.
2. Save the rows in one transaction (`SaveMessageParts`), which locks the
   account's sync state first, as every write to its mail does, so a
   sync write deleting the message waits for it rather than deadlocking.
   A message whose parts are saved already keeps them.

So a row never names content that was never stored. A failure between the
two, or two saves of one message racing before either commits, leaves
content no row names, which the [hourly cleanup](#scheduled-cleanup)
sweeps once it is a day old; a save gives up after an hour, so none in
progress outlasts that margin. The hour is the client's: in the rare case
that a commit lands later still, e.g. after a database outage, a row may
name a blob the sweep deleted, which opening the message repairs as
below. A retry before the rows commit stores the content
again under new IDs: a job's step output may hold no mail, so the IDs
cannot be recorded before the fetch that tells how many parts there are.
Saving a message whose rows are saved stores its content again under the
IDs they name. A row naming content the store no longer has, e.g. after a
lost volume, reads as not stored: fetching the message again and saving
it puts the content back under the same ID. So a lost volume refills
message by message as members open mail, not all at once in the
background. Deleting goes the other way: rows first, then content.
[Content deletion](#content-deletion) deletes the blobs deleted
messages' rows named, after their transaction commits, and a
disconnected account's whole key prefix after the disconnect commits.

Content arrives through `messagecontent.Fetcher`, in three ways:

- [Message bodies](#message-bodies) fetches recent mail in the
  background.
- `FetchMessage` fetches one message as when a member opens it: whatever
  its date or mailbox, and even when its background fetch failed. Saved
  rows stay, so fetching again only puts back text the store lost.
- `FetchPart` fetches an attachment or inline part on first request, or
  any part whose content the store lost. A fresh `messages.get` (60
  units) gives the part's bytes when Gmail sends them in the answer, or
  else its `attachmentId`, since Gmail does not promise an old one keeps
  working, and none is ever taken from a request; then
  `messages.attachments.get` (20 units more, answers up to 64 MiB)
  returns them. A text part's are converted to UTF-8. `Contents.SavePart`
  stores them first, then names the blob in the part's row in a
  transaction that locks the account's sync state. A row that names a
  blob already keeps it, leaving the other copy to the sweep.

Code reaches the store through `messagecontent.Store`, never the SDK. A
`Ref` names one content: the connected account it came from and a blob
ID, which PostgreSQL keeps. The `s3` package alone builds object keys from
it, `accounts/<account ID>/<blob ID>`, so keys hold no addresses,
subjects, or file names. The rules every caller relies on:

- `Put` takes content it can seek in, e.g. a `bytes.Reader` or an open
  file, and stores it from the current offset to the end. A repeated
  `Put` replaces the content, so a retried job may repeat it. After an
  error the object holds the old content or all of the new, never part
  of either, e.g. when an upload was stored but its answer lost; call
  `Put` again until it succeeds.
- `Get` returns a stream the caller closes, or `ErrNotStored` when the
  object is missing. That can happen while a row still names the content,
  e.g. after a lost volume: fetch the content from the provider again.
- `Delete` takes refs, `DeleteAccount` removes an account's whole prefix,
  and `ListOlder` yields the refs last written before a time, for a sweep
  of content no row names. SeaweedFS keeps those times to the second.

`Put` needs content it can seek in because, over plain `http`, the SDK
reads the body once to sign its SHA-256 and again to send it, and seeks
back to retry. SeaweedFS refuses a body that does not match the signed
hash, so an upload is never stored corrupted or in part.

That does not protect against someone who can change the traffic. Over
plain `http` nothing authenticates downloads, listings, or the store's
answers, nor headers the signature leaves out, some of which SeaweedFS
acts on (e.g. `X-Seaweedfs-Extract`, which unpacks a ZIP upload into
other keys); and nothing encrypts content. The Compose files accept
that, trusting the host and a network that only inbok, the worker, and
SeaweedFS share; anywhere else, use an `https` endpoint.

The store's calls fail once a connection delivers no bytes for five
minutes (the SDK's read timeout, which it never sets for S3 by itself),
so a store that takes a request and never answers cannot hold a job or a
request for good; a slow but moving download still ends. An upload taking
over five minutes fails too, since the store sends nothing until the body
is in: fine on a private network, not for a store far away. The timeout counts
silence on a connection, not on one request: over HTTP/2, which Go uses
only with an `https` endpoint, other requests on the connection can keep
one that stalls alive. That can hold a content job, which the sweep
backs up, or a member's download, whose read has no request deadline so
a large file reaches a slow client; `DEBT.md` in the decision records
holds the fix for when an `https` store is used.

Feature tests use `messagecontenttest.Fake`, a store in memory.
`messagecontenttest.RunContract` holds the tests both it and the `s3`
package pass, so the fake behaves as SeaweedFS does; a behaviour every
store must share goes there.

### Public URL

`INBOK_PUBLIC_URL` is the origin users open inbok at: `https`, a host name
or IP address, and an optional port, e.g. `https://mail.example.com`. Plain
`http` is accepted only for `localhost` and loopback addresses, e.g.
`http://localhost:5173` for the Vite dev server. A path other than `/`, a
query, a fragment, or user info stops startup. Host names use letters,
digits, and hyphens; inbok lowercases them and converts an internationalized
domain to its `xn--` form. The example value in `.env.example` is for
development; a deployed server needs its own `https` origin.

Links in inbok's emails start with this value, never with a request's `Host`
header, which a client chooses, so a client cannot point an emailed link at
another server. When moving to another domain, keep the old one for 24
hours, until links sent before the move expire: whoever holds it receives
their secrets. The host is also the name inbok gives mail servers in SMTP's
`EHLO` greeting; an IP address is sent in brackets, e.g. `[192.0.2.10]`.

### Encryption keys

`INBOK_ENCRYPTION_KEYS` is a comma-separated list of `<id>:<key>` entries,
e.g. `2:<base64 key>,1:<base64 key>`. Each ID is a positive whole number
without leading zeros; each key is 32 random bytes in standard base64, from
`openssl rand -base64 32`. Startup fails, without printing key material, if
the list is empty or an entry is malformed or repeated.

The first entry encrypts new TOTP secrets, the copy of a two-factor
enrollment's backup codes kept until the member saves them, a newly
entered system email password, and the refresh token of a newly connected
or reconnected Gmail account with AES-256-GCM. Every entry can decrypt.
Each stored secret records its key ID and is bound to what it belongs to:
a TOTP secret and a backup-code copy to their user; the system email
password to its mail server host, port, and username; a refresh token to
its member and connected account. A ciphertext copied elsewhere does not
decrypt. Backup codes and account passwords use Argon2id hashes and do
not depend on these keys.

To rotate, generate a key, put it first under an unused ID, keep the old
entries, and restart every API instance and worker with the same list.
Existing secrets are not re-encrypted; each keeps its original key until
that factor is removed and enrolled again, and the system email password
keeps its key until an administrator saves a new password, and a refresh
token until its account is reconnected or connected again. A disconnected
account's token keeps its key while it waits for the [account
removal](#account-removal) job; without that key the job drops it without
revoking. Administrators cannot turn TOTP off, so their factors can need an
old key indefinitely. Remove an old key only when no query lists its ID:

```sql
SELECT key_id, count(*) FROM user_totp GROUP BY key_id;
SELECT key_id, count(*) FROM totp_waiting_backup_codes WHERE key_id IS NOT NULL GROUP BY key_id;
SELECT password_key_id FROM system_email WHERE password_key_id IS NOT NULL;
SELECT refresh_token_key_id, count(*) FROM connected_accounts GROUP BY refresh_token_key_id;
SELECT refresh_token_key_id, count(*) FROM account_removals GROUP BY refresh_token_key_id;
```

Back up the key list separately from database backups, so one stolen backup
does not contain both the encrypted secrets and their keys, and so the keys
survive losing the database host. A database restore needs every key its
rows use. If a key is lost, authenticator codes for its users fail with
HTTP 500. Their unused backup codes still sign in, and members can use one to
turn TOTP off and enroll again. Administrators cannot remove the factor
themselves; use host-operator recovery below. A lost key also makes the
system email password unreadable: test messages fail with HTTP 500 and the
settings lose their tested time until an administrator enters the password
again and saves. The web app shows that 500 as a lost test result. A lost
key also makes the Gmail refresh tokens it sealed unreadable: Gmail work
for those accounts fails with `connectedaccount.ErrTokenUnreadable` until
each member reconnects the account, which seals a new token with the first
key. Listing accounts opens each token to tell (`token-unreadable`), so
the Connected accounts page marks such an account and offers Reconnect,
and sending refuses it. Nothing is saved about it, since a server started
with a wrong key list would otherwise mark every account: restoring the
key makes the accounts usable again at the next read.

### Google emulator

`compose.yaml` runs a Google emulator, so connecting Gmail works without a
Google project: [emulate](https://github.com/vercel-labs/emulate)'s
Google OAuth and Gmail API, version 0.13.0, with seeded mailboxes. Its
image is built from `google-emulator/` in the repository with pnpm, since
emulate publishes only an npm package. `.env.example` sets
`INBOK_GOOGLE_BASE_URL=http://127.0.0.1:4000` and the development client
the emulator registers, `inbok-dev.apps.googleusercontent.com`, fixed in
`google-emulator/emulate.config.mjs` so a real client secret in `.env`
never reaches the emulator. `INBOK_GOOGLE_EMULATOR_PORT` moves it to
another port; Compose gives the emulator `INBOK_GOOGLE_BASE_URL` as its
own address, the ID tokens' issuer, so the two agree.

`INBOK_GOOGLE_BASE_URL` makes that one server answer for Google's sign-in
and Gmail hosts, at the emulator's paths: `/o/oauth2/v2/auth`,
`/oauth2/token`, `/oauth2/revoke`, and `/gmail/v1/`. It accepts only
`localhost` or a loopback address, `http` or `https`, and refuses to start
otherwise, or when no client is set: inbok sends that server members'
authorization codes and refresh tokens, so a setting copied from a
development `.env` cannot send them to another machine. For the same
reason, while it is set, the client that carries them connects only to
addresses on this machine, whatever `localhost` resolves to, and ignores
proxy settings; with or without it, that client follows no redirects. A
deployed server leaves it empty. To turn Gmail off, empty all three Google
settings.

The consent page lists the seeded accounts; choosing one agrees to every
permission asked. `google-emulator/emulate.config.mjs` holds the seed:

| Account | Mailbox |
|---|---|
| `alex@example.com` | three messages in two threads, a `Receipts` label |
| `blair@example.com` | four messages, a `Work` label and `Work/Projects` under it |
| `casey@example.com` | one message |
| `unverified@example.com` | empty; its ID token says the address is not verified |
| `testuser@gmail.com` | emulate's own: a few messages and a draft |

The emulator keeps everything in memory. Restarting it, e.g. with
`make services-stop` and `make services-start`, empties the mailboxes
back to the seed and refuses every refresh token handed out before.
Each account keeps its Google ID (`sub`) across restarts.

A patch in `google-emulator/patches/`, which pnpm applies on install,
changes emulate where inbok needs Gmail's behavior: it adds
`users.getProfile`, keeps history IDs within 64 bits, as Gmail's are and
as the Go client decodes them, lets a seed fix an account's `sub`,
refuses a code exchanged by another client or with another redirect URI
than its authorization request's, returns a sent message's other headers,
e.g. `X-Inbok-Message-Id`, and matches `metadataHeaders` in any case. The
install fails if the patch no longer applies, e.g. after a version change;
`google-emulator/pnpm-workspace.yaml` says how to change it. Unpatched,
emulate puts a sent message in the `threadId` it is given, and without one
threads it by `In-Reply-To` and `References` alone, where Gmail starts a
new thread (ADR006, "Replies").

Where it still differs from Google, a test that needs Google's behavior
uses a small `httptest` stub instead:

- A person cannot refuse or untick a permission on its consent page.
- Its revocation endpoint answers 200 for any token, where Google
  answers 400 `invalid_token` for one it does not know.
- `history.list` never answers 404 for an old history ID, and nothing
  returns quota errors. A deletion reaches history only by deleting a
  message for good, which needs a wider scope than inbok's.
- `messages.get` matches `metadataHeaders` only as written, e.g.
  `Message-ID`, and its snippets carry no HTML escaping unless a seed
  sets one.
- `messages.send` keeps the `Message-ID` a client sets, where Gmail
  replaces it with its own (tested 2026-11-09), and it never times out
  or drops a send, so the [send job](#sending-a-message)'s answers and
  its check are tested with stubs too.
- It ignores `access_type` and `prompt` and always returns a refresh
  token.
- It ignores `fields` and answers whole resources, so no test catches a
  field list that leaves out what inbok reads, e.g. history's
  `labelIds`.
- A failed PKCE check leaves the code usable, and posting the consent
  form directly redirects to any `redirect_uri`; only the consent page
  checks it.
- An empty mailbox's history ID is `0`, every ID token carries an `hd`
  claim from the address's domain, and every response allows any origin.

Go tests start their own emulator per test with `internal/googleemulator`,
from the same Dockerfile, tagged `inbok-google-emulator:test` rather than
Compose's `:local`. The first test in a run builds it, never taking an
existing image as found, so a test cannot run a stale emulator. Docker
answers from its cache unless `google-emulator/` changed; the first build
needs network access for the npm registry.

## Reverse proxies

`INBOK_TRUSTED_PROXIES` controls which peers may supply a forwarded client
address for sign-in and setup throttling, for the request limit on public
writes, and for the address saved with each new session. Empty ignores
forwarded addresses and uses the connection's address; behind a proxy, every
session then shows the proxy's address, and all clients share one request
limit, so one busy client can block everyone's sign-in. Trust
only the proxy's address ranges; trusting all addresses lets a caller forge
their throttling address and the sign-in address shown in the session list.

For one nginx proxy on the same host as Go, keep Go on `127.0.0.1:8080` and
set `INBOK_TRUSTED_PROXIES=127.0.0.1/32`. Go serves the web app too (see
The web app), so nginx forwards every path. Adapt the domain and
certificate paths in this HTTPS server block:

```nginx
server {
    listen 443 ssl;
    server_name inbok.example.com;
    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

The example overwrites `X-Forwarded-For` with nginx's client address and
preserves the browser's Host, including its port. nginx forwards `Origin`
and `Sec-Fetch-Site` unchanged. Its [proxy header documentation](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_set_header)
explains the Host override. Keep the Go listener inaccessible to remote
clients. A container proxy has a different connection address; trust its
actual range rather than copying the loopback range. With the Compose
files in `deploy/`, [Using your own proxy](../deploy/README.md#using-your-own-proxy-instead-of-caddy)
gives the address to trust.

## Message parts

An email's body is a tree of MIME parts: a `multipart/*` part holds
others, and each leaf holds one thing, e.g. the text, the same text as
HTML, a PDF, or an image the HTML shows with `<img src="cid:logo">`,
naming the part whose `Content-ID` is `logo`. `messagecontent.Parse`
reads that tree from `messagecontent.Received`, which a provider's
technology package fills (`google` for Gmail), and returns:

- the part tree PostgreSQL keeps, one `Part` per node: the provider's
  part ID (Gmail's `partId`), media type, declared charset, file name,
  size, disposition, `Content-ID`, kind, and, for `multipart/related`,
  its `start` parameter;
- the content of each text part that came with the message, converted to
  UTF-8 by `DecodeFrom`: from the charset the provider says it already
  converted the bytes to (`Received.ContentCharset`; Gmail's inline text
  is UTF-8), or else the part's declared charset, through `DecodeText`. A
  part whose bytes the provider sends separately (Gmail's `attachmentId`)
  has none; decode it with `DecodeFrom` once fetched, with the charset
  `Provider.PartContent` reports. Neither has a text part inside an
  attachment, e.g. a forwarded message, which no view shows.

These are inbok's own rules on top of a parsed tree. MIME libraries such
as emersion's go-message or enmime read raw message bytes and decide no
kinds or display; ADR009 ("Message parts") records why Gmail's parsed
`format=full` is used instead, and where such a library fits later.

Every string in a `Part` comes from the sender, so `Parse` makes it valid
UTF-8 and drops control characters, which PostgreSQL refuses (NUL), and
bidirectional formatting characters, which can make a program,
`photo` U+202E `gpj.exe`, read as `photoexe.jpg`. A message of more
than 1000 parts is `ErrTooManyParts`.

A part's kind decides how a view treats it:

| Kind | Rule |
|-|-|
| `multipart` | a `multipart/*` part, holding others |
| `text` | `text/plain` or `text/html`, not marked `attachment`, no file name |
| `inline` | an `image/*` part with a `Content-ID`, not marked `attachment` |
| `attachment` | everything else, e.g. `text/calendar` or `message/rfc822` |

`DecodeText` reads text as a browser reads a page, through
`x/net/html/charset.DetermineEncoding` (the WHATWG Encoding Standard): a
byte order mark first, then the declared charset, then a `<meta>`
element in the first 1024 bytes, then UTF-8 if those bytes are valid
UTF-8, else windows-1252. `us-ascii` and `iso-8859-1` read as
windows-1252, which senders often mean. A charset the standard calls
unsafe, e.g. `iso-2022-kr`, reads as one U+FFFD, as does each byte that
cannot convert. Like a browser, it garbles UTF-8 labelled as
windows-1252, or undeclared UTF-8 that starts after a first kilobyte of
plain ASCII; both are rare in mail, which declares its charset.

`messagecontent.Choose` picks what a view shows, when the message is
shown, so a better rule reaches stored mail without fetching it again:

- In `multipart/alternative`, the last alternative with HTML, else the
  last with text. The others are the same message in another form, so
  their parts, attachments included, are left out.
- In `multipart/related`, the part its `start` parameter names, else the
  first (RFC 2387). Its inline parts appear where its HTML refers to them
  by `cid:`, listed in `Display.Inline`; when it has no HTML, they are
  offered as downloads. `Display.Downloads` offers after the attachments
  any inline part the HTML never showed. Its other parts are what the
  shown part refers to, so nothing under them is a body: an image is
  inline as above, anything else is offered as a download.
- In any other multipart, every part in order: several text parts show
  one after another, and attachments, plus inline parts outside
  `multipart/related`, are offered as downloads.

Reports say Gmail's `attachmentId` changes between `messages.get` calls,
so a part is named by its message and part ID, and its bytes are fetched
with the `attachmentId` of a fresh `messages.get`; an attachment's bytes
may also come as `data` in that answer. Real Gmail, recorded from the test
account in `internal/google/testdata/recorded`:

- converts a text part's inline `data` to UTF-8, from its declared
  charset, its `<meta>` charset, or else Latin-1, yet keeps its
  `Content-Type` and its `size` as sent, so the `google` package marks it
  with `ContentCharset` `utf-8`;
- sends a long text part, and every file and inline image however small,
  by `attachmentId`, and `messages.attachments.get` returns those bytes as
  sent;
- decodes RFC 2047 and RFC 2231 file names, rewriting the part's header
  fields to match;
- nests a forwarded message's parts under its `message/rfc822` part.

The other fixtures in `internal/google/testdata` are built in that shape.
[Gmail fixtures](../CONTRIBUTING.md#gmail-fixtures) says how to record
more.

## Showing HTML

Content storage keeps a message's HTML as the sender wrote it. Each time
a view shows it, `messagecontent.Sanitize` streams a copy that is safe to
show, so a better rule reaches stored mail at once. It takes a `Policy`:
the inline parts the HTML may show (`Display.Inline`), a function that
builds a part's URL from its part ID alone, and whether remote images
load (`RemoteBlocked`, the zero value's behaviour, or `RemoteAllowed`).
The copy:

- keeps sectioning, text, list, table, and image elements and the
  presentational ones mail still uses, e.g. `center` and `font`, with
  layout attributes, `class`, `id`, and `aria-*`. Scripts, forms,
  embedded content, SVG, MathML, comments, and the sender's `<meta>`,
  `<base>`, and `<link>` are gone; an unknown element keeps its text.
- keeps links, `href` on `a` only, with absolute `http`, `https`,
  `mailto`, and `tel` URLs, opening in a new tab. A relative URL would
  resolve against inbok, and a URL with a space in it is dropped, as
  bluemonday drops it; a scheme-relative one, `//host/path`, reads as
  `https`.
- turns `cid:` images and backgrounds into the part's URL, and drops
  remote ones unless allowed, reporting them in `Sanitized.Blocked` so the
  view can offer to load them. Only `img`'s `src` and the `background`
  of `body` and table elements load an image. `data:` URLs are dropped.
- keeps `<style>` and `style` attributes, filtered to the CSS properties,
  selectors, and media queries Gmail documents. A `url()` that may not
  load becomes `none`, so a background color still applies. `@import`,
  `@font-face`, escapes, and functions such as `var()` are dropped, and
  so is a style sheet a screen would not apply, e.g. `media="print"`. A
  style element's `media` keeps the queries an `@media` rule may have.
  A length in units of the viewport's height or logical axes, e.g.
  `min-height:100vh` or `100vi`, drops its declaration: the message view
  sizes its frame to the body, so such a length would grow with the
  frame and cut the body off.
  CSS is written again with a space between tokens, so tokens the
  parser read apart, e.g. across a comment it dropped, cannot join into
  a comment or a name in the browser. At most 256 KiB of CSS is read per
  document, since the parser holds a declaration's tokens at once.
- starts with `<!DOCTYPE html>`, so the browser lays it out in standards
  mode, as Gmail does.

`Sanitized.Shown` lists the inline parts the HTML showed;
`Display.Downloads` offers the rest as downloads after the attachments.
Like `search.BodyText`, the sanitizer reads tokens and never builds a
tree with `html.Parse`, which a crafted document can make use hundreds
of megabytes. It writes every token again itself, so a browser reads the
copy as the same tokens. A fuzz test checks that every copy parses into
allowed elements only, that its CSS has no comment, at-rule but
`@media`, or `url()` it may not load, that bluemonday's `UGCPolicy`,
widened only by the extras mail needs, would remove nothing from it,
and that sanitizing a copy without inline parts changes nothing (a
second copy drops the relative part URLs of the first); run it longer
with `go test -fuzz=FuzzSanitize ./internal/messagecontent`.

The web app shows each HTML body in a sandboxed iframe that loads the
body from its own URL; `httpapi`'s `messageBody` builds that response.
It sanitizes the whole copy first, so a store that fails midway gets an
error answer rather than a message cut short, then sends it as
`text/html; charset=utf-8` with `Cache-Control: no-store` and a
Content-Security-Policy of its own, a second layer behind the sanitizer:

```text
sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox;
default-src 'none'; style-src 'unsafe-inline'; form-action 'none';
base-uri 'none'; frame-ancestors 'self'; img-src 'self'
```

Nothing in the body runs, and it loads nothing but its own CSS and
images from inbok's origin, which `withFetchDest` limits to part URLs
(see [API contract](#api-contract)); when remote content is allowed for
the view, `img-src` adds `http:` and `https:`. `allow-same-origin` keeps
the body on inbok's origin, so inline images load with the session
cookie and the web app can size the frame. The response replaces the
`X-Frame-Options: DENY` of [Security headers](#security-headers) with
`SAMEORIGIN`. A `srcdoc` frame would take the web app's policy instead,
which refuses the message's CSS; ADR009 records the trade-offs.

Three operations under the Messages tag serve a message's content, each
through `messagecontent.Reader`, which finds the message only in the
signed-in member's own connected accounts:

- `GET /messages/{id}/content` returns what the view shows, as
  `messagecontent.Choose` picks it: the bodies in order, whether an HTML
  body refers to remote images, and the downloads. An HTML body comes as
  two body URLs, remote images blocked and allowed; the reader runs
  `Sanitize` into `io.Discard` to learn `Blocked` and `Shown`, the inline
  parts the HTML shows, which `Display.Downloads` leaves out. A plain-text
  body comes inline, clipped at 102 KiB on a character boundary with
  `clipped` set, and its part URL holds the whole text.
- `GET /messages/{id}/bodies/{part}` returns `messageBody` for an HTML
  part the view shows as a body, with `?remote=allowed` for the copy
  that loads remote images. It lists `iframe` under `fetchDestKey`.
- `GET /messages/{id}/parts/{part}` returns a part the view shows or
  offers, as stored: a text body in UTF-8, anything else as received. It
  serves PNG, JPEG, GIF, and WebP inline with `nosniff`, and anything
  else, SVG included, as an attachment under
  `Content-Security-Policy: sandbox; default-src 'none'`. It lists
  `image`, `document`, and `empty` under `fetchDestKey`: an image in a
  body, a part opened in a tab, and a download link and the web app's
  calls, which both send `empty`.

`{part}` is the part's place, `0` for the top part and then each index on
the way down, e.g. `0.1.2`, the `path` of its row; Gmail's `partId` of
the top part is empty, which a path segment cannot carry. Content not
stored, e.g. of a message older than the background fetch's 90 days or
an attachment never downloaded, is fetched in the request through
`FetchMessage` or `FetchPart`, with `serve`'s own Google client at its
own pace. That keeps the request's 10 seconds: a provider that keeps
refusing answers 503 `mail-provider-busy` with `Retry-After: 30`, or the
shared `service-busy` once the backoff outlasts the request. The part
route reads the stored copy without that deadline and moves the write's
deadline 5 minutes on, so a large attachment reaches a slow client; the
store's read timeout still ends a read that stalls.

## Email addresses

Every address of inbok's own goes through `emailaddress.Parse`: setup,
sign-in, `recover-admin`, registration, the system email From address, each
system email's recipient, and a connected Gmail account's email from Google.
It trims surrounding spaces and tabs, accepts one bare address of at most
254 octets in UTF-8 (RFC 5321's limit) without hidden characters, and
lowercases the letters A to Z, local part included; other letters keep their
case. Hidden characters are controls, invisible formatting such as a
zero-width space or a right-to-left override, other characters Unicode
draws as nothing, such as a variation selector or a Hangul filler, and
Unicode spaces and separators. Anything else
is `emailaddress.ErrInvalid`, which handlers answer with a 422 on the field;
Huma accepts hidden characters, so for those the service's 422 is the one a
client sees. Saved addresses are compared
exactly, and the `users` and `connected_accounts` tables refuse an email
with an uppercase ASCII letter. New code that takes an address calls
`Parse` in its service.

The recipients of a member's own mail go through `emailaddress.ParseRecipient`
instead: the same rule and trimming, but every letter keeps its case, since
the recipient's server may treat the local part's case as meaningful
(RFC 5321) and inbok only sends to them.

ADR025 in the project's decision records explains the choice and its cost: a
mail server that treats the local part's case as meaningful is not
supported for inbok's own addresses. ADR006 explains why connected Gmail
accounts follow the rule, and why recipients do not.

## Password hashes

Passwords and TOTP backup codes are stored as Argon2id hashes
(`passwordhash`), with OWASP's minimum profile: 19 MiB, two passes, one
lane. A check uses the stored hash's own costs, so changing the profile
keeps old passwords usable; it refuses a stored hash that asks for more
than 128 MiB, 10 passes, or 16 lanes.

Hashes and checks in progress share a 128 MiB memory budget across the
process: each waits until its own memory cost fits, in arrival order, so
6 hashes of the current profile run at once. The budget counts what
hashes ask for, not what the process holds: the garbage collector frees a
finished hash's memory later, so under load the process briefly holds
more. Every caller shares it:
sign-in, setup, registration, password reset, TOTP backup codes, and
`inbok recover-admin`.
A wait that outlives the request's context ends it; one that outlives the
10-second request deadline is a 503 `service-busy` (see
[Timeouts and shutdown](#timeouts-and-shutdown)). A TOTP request that ends
while waiting to check its first backup code made no guess, so it gives
its attempt back.

## First administrator setup

After applying migrations, create the first administrator through the web app:

1. Generate a random token with `openssl rand -base64 32`.
2. Set `INBOK_SETUP_TOKEN` in `.env` to that output, then start or restart
   the backend. The Make commands load `.env`; direct Go commands need the
   shell-loading step shown in Run the server.
3. Open `http://localhost:5173/setup` with the web app running. Enter the
   token, an email address, and a password of 15 to 128 Unicode code points.
   Confirm the password in the form.
4. After setup succeeds, remove the token from `.env` and restart the backend.

An unset or empty token leaves setup unavailable while no administrator
exists. A nonempty token shorter than 32 Unicode code points stops `serve`
before it connects to PostgreSQL or opens the HTTP port. Token comparison
preserves the configured contents, including whitespace. The server never
logs the token or returns it in an error.

`GET /api/v0/setup` reports `unconfigured`, `open`, or `done`. An existing
administrator always means `done`, even after the token is removed.
`POST /api/v0/setup` creates one administrator atomically; concurrent claims
cannot create a second one. Setup creates no workspace or membership. It
signs the new administrator in with a 15-minute session that can only set
up two-factor authentication (`session.Service.SignInFirstAdmin`), the same
session sign-in gives an administrator without it. That sign-in runs after
the administrator is saved: if it fails, the request answers 500 and the
administrator signs in as usual. If the token remains configured after
setup, `serve` logs a startup warning asking the operator to remove it.

The web app opens two-factor setup after completion and shows the
token-removal instructions there. Before setup is complete, every web page
leads to setup. After completion, `/setup` returns to the normal entry
page. The API's database claim enforces setup completion
independently of these browser redirects.

## Sign-in and browser sessions

`POST /api/v0/sessions` accepts email and password. Without confirmed TOTP, it
creates a fresh session and returns HTTP 201 with
`{"data":{"step":"complete"}}`. With confirmed TOTP, it returns
`{"data":{"step":"totp"}}` and a temporary `__Host-inbok-challenge` cookie; no
session is created. The factor check and the chosen write share one user lock,
so finishing two-factor enrollment cannot race a password-only session into
existence afterward. Unknown email and wrong password return the same HTTP
401; unknown users still incur an Argon2id password check. A deactivated
account with the right password returns HTTP 403 `account-deactivated`
instead; see [Member management](#member-management).

`POST /api/v0/sessions/totp` accepts a string `code` and the challenge cookie.
Six ASCII digits select an authenticator code; other input is checked as a
backup code. Success atomically consumes the challenge and the TOTP step or
backup code, creates a session, and clears the challenge cookie. Only then
does the response issue the session cookie. A failed commit reveals no
session secret and leaves the challenge and code usable.

Challenges expire after five minutes. Wrong codes update their saved failure
count; the fifth deletes the challenge and requires password sign-in again.
Starting a new challenge does not reset the shared per-user code allowance:
5 failed or pending attempts per 15 minutes and 20 per 24 hours. Successful
code sign-in removes only its own attempt in the same transaction. A code
request refused before any code is checked, e.g. with an expired challenge,
TOTP off, or as an administrator turning it off, releases its attempt: only
guesses count.

Expired challenges are pruned for that user during a successful password
sign-in. Saving a new session, after a password or a code, also deletes that
user's expired sessions in the same transaction. There is no scheduled
cleanup; idle users' expired rows remain unusable until removed. Both cookies
use Secure, HttpOnly, SameSite=Lax, and Path=/, without Domain.
Authentication responses use Cache-Control: no-store.

`GET /api/v0/sessions/current` returns the current email, role,
`totpRequired`, and `emailVerified`. An administrator with confirmed TOTP receives admin access
and `totpRequired: false`. An administrator without confirmed TOTP receives
enrollment access and `totpRequired: true`: only the current session,
sign-out, and the two-factor enrollment operations accept it, every other
operation answers 403 `totp-required`, and its session lives 15 minutes.
Finishing enrollment replaces it with a full session. Members always
receive member access and `totpRequired: false`, whether or not they enable
TOTP. Operations declare their level in `accessKey` metadata
(`session.AccessEnrollment`, `AccessMember`, `AccessAdmin`); missing
metadata means admin.

Identity reads the current account and factor through the session row in one
query. A session revoked by finishing two-factor enrollment cannot gain admin
access from a previous session check. The web app's two-factor page,
`/settings/security/two-factor`, handles setup, backup codes, and turn-off;
an administrator without two-factor sees setup alone at
`/two-factor-setup`; sign-in shows the code step.

`DELETE /api/v0/sessions/current` deletes the checked session and clears its
cookie, returning HTTP 204. A missing or expired session returns HTTP 401
and clears the cookie, also when a handler finds the session ended after
the access check; the web app treats that as signed out. Signing
out clears account data and submitted credentials from the web app's Query
cache while retaining the public setup status.

Each new session saves the request's `User-Agent`, cut to 512 bytes, and the
client address resolved as in [Reverse proxies](#reverse-proxies). They help
the owner recognize a session. No access decision uses them, because the
client chooses its `User-Agent`. Sessions created before these columns
existed have neither. Both are personal data: an expired row keeps them until
its user signs in again, with no time limit for users who never return.
Operators with a retention duty must delete expired rows themselves.

`GET /api/v0/sessions` lists the account's live sessions, newest sign-in
first, in `data.sessions`. Each has an `id`, `browser` and `os` names read
from the saved header, `signedInAt`, `lastActiveAt`, `signedInFrom`, and
`current` for the session that made the request. Unrecognized names and an
unknown address are empty strings. `lastActiveAt` changes at most hourly, as
described below. The `id` is a random UUID that names the session to its
owner; the token hash never leaves the server. Listing deletes nothing.

`DELETE /api/v0/sessions/{id}` ends another session of the account and
returns HTTP 200 with `{"data":{"ended":1}}`. A missing, expired, or other
account's session returns the same HTTP 404 (`session-not-found`); an
expired one is deleted as well. The current session returns HTTP 409
(`session-is-current`); sign-out ends it. `DELETE /api/v0/sessions/others`
ends every other session of the account and returns how many live sessions it
ended, which can be 0. Both are safe to repeat. Ending a session stops its
next request, not one already running, and does not stop someone who knows
the password from signing in again; there is no password change yet.

An administrator can deactivate a member, which ends all of that member's
sessions; see [Member management](#member-management). Member recovery is
not implemented yet. Host-operator administrator recovery is described
below.

The cookie is `__Host-inbok-session`, with `Secure`, `HttpOnly`,
`SameSite=Lax`, `Path=/`, and no `Domain`. `Max-Age` is the remaining maximum
session lifetime. Go sets these attributes explicitly; the name alone does
not configure them. See the [cookie attribute reference](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie).
Use HTTPS for deployed instances. For local development, use
`http://localhost:5173`; browsers provide a localhost exception for Secure
cookies. If the browser rejects the cookie on plain HTTP, use local HTTPS
rather than disabling Secure or changing the cookie name.

Session rules are constants, not environment settings:

- Idle expiry is 7 days since the stored last-use time; maximum lifetime is
  30 days from sign-in and is never extended. A session with enrollment
  access, an administrator's without confirmed TOTP, lives 15 minutes from
  sign-in instead (`enrollmentSessionLifetime`).
- Each authenticated request loads the session and current user from
  PostgreSQL. It updates last-use time only when at least an hour has passed
  since the stored value. This reduces writes, not reads.
- Expiry is checked before that update. An expired row is deleted when used
  or when its user signs in again; there is no cleanup job. Sign-out deletes
  the current row.
- Every sign-in creates a new 32-byte random secret. PostgreSQL stores only
  its SHA-256 hash. Cookie secrets, passwords, and hashes must not be logged.

Throttling uses PostgreSQL across API instances. Within 15 minutes, at most
5 failed sign-ins are allowed for one normalized email and client, and 50
failures for one client across all emails. A client is an IPv4 address or
an IPv6 /64 (`clientprefix`), as in every limit. Setup attempts share the
client limit. A pending attempt counts until a successful verification
removes it, so parallel password checks cannot bypass the limits. A blocked
attempt returns HTTP 429 with `Retry-After`: the seconds until one more
attempt fits, rounded up. These limits are constants too.

Every throttle (sign-in, setup, TOTP codes, verification links,
registration, and password reset links) counts with `attemptlimit.Wait`,
which says how long until one more attempt fits; a new throttle uses it
too. Each keeps its own key, storage, and lock: under the lock it counts the
earlier attempts, then saves this one only if it fits. A
refusal is an `*attemptlimit.Error` that wraps the feature's `ErrThrottled`
and carries the wait; `retryAfterProblem` in `httpapi` turns it into the
header. Every documented 429 lists the header as required, so a new 429 must
send it.

In front of those throttles, every write that needs no session (`rateLimited`
in `httpapi`) passes a limit kept in memory, per operation and client: up to
20 requests in a row, and each second gives one back, up to 20. A client is
an IPv4 address, or an IPv6 address's /64 block (`clientprefix`). Past the
limit, the request gets HTTP 429 `too-many-requests` with `Retry-After`
before any database work, so a flood cannot hold database connections or
start password hashes. Over
any 15 minutes it allows more requests than any database limit, so people
retrying by hand meet those limits first. Counts are per process and reset on
restart; at most 100,000 clients are tracked at once. A new public write gets
the limit, and its 429 in the contract, with no code of its own.

`DELETE /api/v0/totp/enrollment` cancels a signed-in account's pending
enrollment without a code. Success returns HTTP 200 with `data.status:
"off"` and `data.backupCodesLeft: 0`; repeating it when no factor remains
also succeeds. A confirmed authenticator returns HTTP 409 (`totp-already-on`).
Existing sessions and their cookies stay unchanged.

`POST /api/v0/totp/backup-codes` requires a signed-in account and a fresh
six-digit authenticator `code`. It replaces all old backup codes and returns
HTTP 200 with `data.backupCodes`, containing ten replacement codes shown once.
Backup codes cannot authorize this operation. Existing sessions stay signed
in, and failure preserves the old set. The shared code-attempt limits apply.

`POST /api/v0/totp/disable` lets a signed-in member turn off TOTP with a
fresh authenticator code or an unused backup code in `code`. Success returns
HTTP 200 with `data.status: "off"` and `data.backupCodesLeft: 0`. The factor
and every backup code are removed together; existing sessions stay signed
in. Administrators receive HTTP 403 and must keep TOTP enabled.

## Administrator recovery

Only a trusted host operator should run this command for an administrator
who lost their password, authenticator, and backup codes. Before recovering
another administrator, verify their identity through your deployment's
operator process. The command trusts database write access; it does not ask
for an inbok session, old password, TOTP code, or setup token, and it does not
check which machine runs it. Members cannot be recovered this way.

An administrator who only forgot their password can use
[Password reset](#password-reset) instead, once their email is verified.
Setup does not verify the first administrator's email, so one who forgets
their password before verifying it needs this command.

Apply migrations first, including the workflow tables `migrate` creates:
the command saves the administrator's notice there. The server may keep
running. From the repository root:

```sh
set -a; . ./.env; set +a
go -C backend run ./cmd/inbok recover-admin admin@example.com
```

Only `INBOK_DATABASE_URL` is required. Terminal input prompts twice for a
password of 15 to 128 Unicode code points without echo, then asks
`Continue? [y/N]`. Only `y` or `yes` proceeds. With piped input, the command
reads the first line and skips confirmation. Password spaces are preserved.
Use a password manager or another trusted program to supply that line; never
put the password in arguments, environment variables, or shell history.

In a container from the image, the binary is on the `PATH`. The
container must have `INBOK_DATABASE_URL` set, as one running `serve` does.
Replace the container name with yours:

```sh
docker exec -it inbok inbok recover-admin admin@example.com
```

For piped input, use `docker exec -i` without `-t` so it uses the one-line
input mode. This repository's development Compose file starts PostgreSQL,
Mailpit, and the Google emulator; it does not run inbok itself.

Recovery checks that the account is an administrator, then replaces the
password, removes TOTP and every backup code, ends all sessions and sign-in
challenges, clears that account's code and sign-in attempt rows, deletes its
password reset links, records an `admin-recovered` event in `audit_events`,
and saves the [recovery notice](#recovery-notice) for the worker, with that
event's time. These writes commit together: a recovery that fails saves no
notice, and one that succeeds always has one. Counts are printed without
secrets, followed by whether the administrators will be emailed:

```text
Every active administrator will be emailed about this recovery once the worker runs.
```

or, when system email is not configured or has not passed a test
message, that the administrators will be emailed only if it is fixed
before the worker tries: the notice is saved either way, and the worker
reads the settings when it sends. Recovery itself sends nothing and needs
no encryption keys.

The administrator signs in with the new password and gets a 15-minute
session with enrollment access, which can only set up two-factor
authentication; the web app opens that setup at once. Finishing the
enrollment restores administrator access. A sign-in that verified the old
password before recovery cannot create a session afterward, even if
recovery sets the same password again.

Recovery leaves `users.deactivated_at` as it is. The web app cannot
deactivate an administrator, but one deactivated directly in the database
stays unable to sign in after recovery; clear the column in SQL as well. A
deactivation done in SQL also leaves the account's sessions and sign-in
challenges in place, refused only while the column is set, and records no
audit event; delete them too.

If the command reports unconfirmed success after interruption or a database
error, inspect the audit table before retrying. It contains no credentials:

```sql
SELECT id, occurred_at, action, target_user_id
FROM audit_events
WHERE action = 'admin-recovered'
ORDER BY id DESC;
```

## System email

System email is the mail server inbok sends its own messages through,
separate from members' mailboxes. It has no environment variables, because
a test send must pass on the current settings before registration opens and
each save is audited; US018 records the rule. An administrator with
confirmed TOTP configures it on the web app's Settings page, which calls
`GET` and `PUT /api/v0/system-email` and
`POST /api/v0/system-email/test-message`. It sends test messages and
[email verification](#email-verification) links.

The settings are one `system_email` row: host, port, security, an optional
username and password, and the From address and name. Security is `tls`
(TLS from the first byte, usually port 465), `starttls` (required: a server
that does not offer it fails the send, usually port 587), or `none`. `none`
sends the whole message unencrypted and unchecked, so use it only for a
server on the same host or a trusted private network. It allows no username, and inbok never signs in
without TLS, so the password never crosses the network in clear text. TLS
checks the server's certificate against the host name with the system's
roots and needs TLS 1.2 or newer. The host, username, and From address are saved
without surrounding spaces, so a username of only spaces means no sign-in.

The password is write-only. Reads return `hasPassword`; the value is
encrypted with `INBOK_ENCRYPTION_KEYS` and bound to the host, port, and
username it was entered for. A save without a password keeps the saved one
only when those three are unchanged, so an administrator cannot move it to a
server they control and read it there. Clearing the username clears the
saved password.

Saving replaces the whole row, clears the tested time, and records a
`system-email-saved` event in `audit_events` with the saving administrator
as `actor_user_id`, in the same transaction. The event holds no setting
values. A test message sends a short fixed message to the given recipient
with the saved settings. A pass sets the tested time. A `failed` result
clears it, even when only the recipient was refused, and so does a password
that no longer decrypts. Tests are not audited. A test that ends after a
newer save leaves that save's tested time alone.

A test answers `accepted` or `failed` with the step that failed: `connect`,
`tls`, `auth`, `sender`, `recipient`, or `timeout`. Accepted means the server
took the message for delivery, not that it arrived. One send, from dialing to
the server's last reply, has at most 8 seconds, inside the 10-second request
limit. A failed send logs a warning with its purpose, e.g. `test message`,
the reason, host, port, security, username, and the first 512 bytes of the
error. inbok never adds the password; the text can include the server's own
reply, except at sign-in. A reply to the sign-in can quote the Base64
credentials inbok sent, the password included, so a failed sign-in keeps
only the reply's code and enhanced code, e.g. `SMTP error 535: 5.7.8`, or
`unexpected reply to the sign-in` for a reply it cannot use. A later reply
keeps its text, but the credentials' Base64 lines become `[credentials]`
and the recipient's address, in any letter case, becomes `[recipient]`,
e.g. `SMTP error 550: <[recipient]> unknown`, so the logged reply carries
neither; a malformed one keeps none of its text. The warning carries the
`request_id` of the request that sent it.
Two choices each email makes decide what a send does:

- **Private** when its body holds a secret, something that grants access if
  read, e.g. a link's token: the warning then leaves out the server's reply,
  which can quote the message. Reset, registration, and verification links
  are private; notices and test messages are not.
- **Tested settings required** for mail nobody asked for, e.g. the
  [recovery notice](#recovery-notice) to other administrators: it is not
  sent, or retried, through settings no one has seen work. Mail someone
  asked for or caused, e.g. a link or the notice of their own reset, goes
  out whenever settings are configured, so a saved but untested change
  does not stop it.

```text
time=2026-11-01T10:00:00.000+07:00 level=WARN msg="system email send failed" purpose="test message" reason=auth host=smtp.example.com port=587 security=starttls username=inbok@example.com err="send failed at auth: SMTP error 535: 5.7.8" request_id=0b9d6a56-3f4e-4c1a-9d2b-7e8f0a1b2c3d
```

A send is **unconfirmed** when inbok had written the whole message, final
dot included, and then failed while waiting for the server's answer
without a 4xx or 5xx refusal: the connection dropped, time ran out, the
reply was garbled, or the caller gave up. The server may have accepted
the message, so the warning adds `unconfirmed=true`, and the `SendError`
says so to callers, also when it matches the caller's cancellation;
sending again could deliver it twice. A 4xx or 5xx refusal, and any
failure before the final dot was written, is not unconfirmed: the message
was not accepted.

A refusal with a 5xx reply is **permanent**: RFC 5321 says the same
command gets the same answer, so the `SendError` marks it for a caller
that retries, which can stop. A 4xx reply, e.g. greylisting, and a
dropped connection are not. Servers do not always follow the RFC, e.g. a
full mailbox can answer 552 and later accept, so a caller decides per
step whether to trust it. For a message that is not private, the reply,
code included, is in the warning's `err`; for a failed sign-in, only the
code.

To list saves, with no setting values:

```sql
SELECT id, occurred_at, actor_user_id
FROM audit_events
WHERE action = 'system-email-saved'
ORDER BY id DESC;
```

### Testing with Mailpit

`make services-start` also starts Mailpit, which accepts any message and
shows it in a web page instead of delivering it. On the Settings page, save:

| Field | Value |
|---|---|
| Mail server | `127.0.0.1` |
| Port | `1025`, or `INBOK_MAILPIT_SMTP_PORT` from `.env` |
| Security | None |
| Username and password | empty |
| From address and name | any, e.g. `inbok@example.com` and `inbok` |

Then send a test message and open `http://localhost:8025`
(`INBOK_MAILPIT_WEB_PORT`) to read it. Verification links arrive there too.
Mailpit deletes its messages when it stops and keeps the newest 500.

## Email verification

A signed-in account proves it controls its email address by following a
link inbok emails to it. Nothing sends a link automatically: the user asks
for one on the web app's Security page. An unverified account signs in and
works as before; `GET /api/v0/sessions/current` reports `emailVerified`.

`POST /api/v0/account/email-verification` sends a link to the session's own
account and returns the address it went to. It answers 409
`email-already-verified`, 409 `system-email-not-configured` before system
email is saved, or 429 `email-verification-throttled` after 3 links in 15
minutes or 10 in 24 hours, with `Retry-After`, like the other limits. The
limit counts the account's saved links, so anything that deletes them, such
as a redeem, starts it over. The link is saved before the send, so a 502
`email-not-sent` still counts toward the limit, and an
[unconfirmed](#system-email) message may arrive and work. A failed send logs the same warning as a test message, with
`purpose="email verification"`, but without the server's reply, which can
quote the link. A test message to the same server shows the reply, except
at sign-in, where it shows only the reply's code.

The link is `INBOK_PUBLIC_URL` + `/verify-email#` + a 32-byte random secret
in unpadded base64url. Browsers never send the part after `#` to a server,
so the secret stays out of inbok's and its proxy's request logs and out of
Referer headers. It still sits in the email, in the browser's history, and
with any service that rewrites links in mail. Only the secret's SHA-256 hash
is saved, in `email_verification_links`, with the email it was sent to. A
link expires after 24 hours. Every unexpired link works until one is used.

`POST /api/v0/email-verifications/redeem` takes the secret, needs no session,
and signs no one in. It marks the account verified, records an
`email-verified` audit event with the account as `target_user_id` and no
actor, and deletes every link of that account. A link for an account that is
already verified succeeds without changing its verified time or recording an
event. An unknown, used, or expired link, or one sent to an address the
account no longer has, answers 404 `email-verification-link-invalid`. The
address must match exactly; both are saved lowercased, as
[Email addresses](#email-addresses) describes. Redeem has no attempt limit:
a 256-bit secret cannot be guessed.

A deactivated account cannot ask for a link: the request answers like a
signed-out one, HTTP 401. Deactivation deletes its unused links, so a link
sent before it answers 404. Sending a link deletes the account's expired
links; a refused request or redeem changes nothing. The worker's hourly
[cleanup](#scheduled-cleanup) deletes every account's expired links.
Deleting an account deletes its links.

To list verifications:

```sql
SELECT id, occurred_at, target_user_id
FROM audit_events
WHERE action = 'email-verified'
ORDER BY id DESC;
```

## Member management

An administrator with admin access lists every account and deactivates or
reactivates members. Administrators themselves, the signed-in one included,
cannot be deactivated or reactivated, so an instance never loses its last
administrator this way. Deactivation keeps the account; nothing deletes a
user.

`GET /api/v0/members` returns every account, oldest first, in
`data.members`. Each has its `id` (the user ID), `email`, `role`,
`joinedAt`, `emailVerified`, `totpEnabled`, `deactivatedAt`, null while
active, and `allowedActions`: `deactivate` and `reactivate` for a member
(either may change nothing), none for an administrator. Deactivate and
Reactivate refuse through the same rule (`account.Member.AllowedActions`).
It holds nothing about mail, sessions, or activity. There is no paging.

`POST /api/v0/members/{id}/deactivate` and `.../reactivate` return HTTP 200
with the member as saved in `data.member`. An unknown ID answers 404
`member-not-found`, an administrator 409 `member-is-administrator`, and an
ID that is not a positive whole number 422 at `path.id`. Both are safe to
repeat: a member already in the asked state is returned unchanged, with no
second audit event.

Deactivation locks the user, the same lock sign-in, TOTP, email
verification, and password reset take, and checks the member's role under it.
In one transaction it sets `users.deactivated_at`, deletes the member's
sessions, pending sign-in challenges, unused email verification links, and
unused password reset links, and records a
`member-deactivated` event with the administrator as `actor_user_id` and
the member as `target_user_id`. It keeps the password, the TOTP factor and
backup codes, and the verified address, so reactivation, which clears the
column and records `member-reactivated`, restores the account as it was.

A deactivated account is kept out at each step:

- Sign-in hashes the password before taking the user lock, then reads the
  flag under it, so a deactivation that commits in between still counts. The
  right password answers HTTP 403 `account-deactivated`, "This account is
  deactivated. Ask your administrator."; a wrong one still answers 401 and
  counts toward the throttle. Only someone who knows the password learns the
  account is deactivated.
- TOTP changes and asking for a verification link answer HTTP 401 if a
  deactivation committed after their session check. Redeeming a link answers
  404.
- The session check requires an active account, and the code step answers
  the same 403 and deletes the challenge. Deactivating through the API deletes
  every session and challenge, so these two matter only when
  `deactivated_at` is set directly in the database.

To list deactivations and reactivations:

```sql
SELECT id, occurred_at, action, actor_user_id, target_user_id
FROM audit_events
WHERE action IN ('member-deactivated', 'member-reactivated')
ORDER BY id DESC;
```

## Registration

While an administrator keeps registration open, anyone who can reach inbok
can create a member account. A visitor gives only an email first. The
password comes after they follow a link emailed to them, so no one can
choose the password of an email they do not control, and no half-made
account ever exists. ADR028 in the project's decision records explains
each choice below and the alternatives it was chosen over.

### Opening and closing

`GET` and `PUT /api/v0/registration/settings` need administrator access.
`PUT` takes `{"open": true}` or `{"open": false}`. Registration starts
closed. Both answer `{status, systemEmailTested, allowedActions}`:
`allowedActions` is `close`, plus `open` once the system email settings in
use passed a test, the same rule `PUT` refuses through.

- Opening needs system email settings that passed a test message;
  otherwise it answers 409 `system-email-not-tested`. Closing always
  works.
- Every save records `registration-opened` or `registration-closed` with
  the administrator as `actor_user_id` and no target, even a save that
  changes nothing.
- Saving new system email settings, or a failed test, clears their tested
  time. Registration then reports `paused`: still open, but no visitor can
  register until a test passes. Nothing closes it behind the administrator's back.
- Closing deletes every registration attempt and link, so no visitor's
  email or IP address is kept while it stays closed. The cost: a link sent
  before the close stops working even after registration reopens, and the
  limits below start from zero.

`GET /api/v0/registration` is public and answers `{"open": true}` only when
visitors can register now; paused answers `false`.

### Asking for a link

`POST /api/v0/registration/links` takes `{"email"}` and answers 200
`{"status": "requested"}` whatever happens to that email:

- an email without an account gets a link,
  `INBOK_PUBLIC_URL/register/complete#` + a secret, made and stored like an
  [email verification](#email-verification) link (only its SHA-256 hash is
  saved, in `registration_links`), which expires after 24 hours;
- an email with an account gets a notice instead: someone tried to register
  it, and here is the sign-in page. A deactivated account gets it too, since
  the answer must not depend on the account's state;
- an email that got 3 messages in the last hour, or 5 in 24 hours, gets
  nothing. Links and notices count; requests that sent nothing do not.

The same answer for all three keeps the response from telling whether an
email has an account. Other answers:

- 409 `registration-closed`: registration is closed or paused.
- 429 `registration-throttled` with `Retry-After`: this client (an IPv4
  address or an IPv6 /64) asked 10 times in 15 minutes, or everyone
  together asked 50 times in the last hour. Both limits are checked before
  the email's own, so a 429 never depends on the email. A refusal by the
  limit on everyone logs a warning,
  `msg="registration limit on everyone reached" wait=...`, since it refuses
  every visitor and may mean someone is keeping it full.
- 502 `email-not-sent`: the send failed. The link stays saved and counts,
  and an [unconfirmed](#system-email) message may still arrive. The
  warning logged has
  `purpose="registration link"` or `"registration notice"` and no server
  reply.

The email counts exactly as `Parse` returns it: `kate+news@example.org` and
`kate@example.org` are two emails. Folding them would need each provider's
rules and can hand an account to the wrong person, so it is left out.

Two differences remain, and ADR028 accepts both: a request that sends
nothing answers sooner, and while mail is failing it still answers 200
where the others answer 502. Either shows that the email was asked about
recently, never whether it has an account. Anyone can also use up the 50
per hour and block registration for an hour; `DEBT.md` lists the defenses
for when that happens.

How it runs: every request first reads whether registration is open, so a
closed inbok answers without waiting. It then waits for one PostgreSQL
advisory lock that all registration requests share
(`LockRegistrationAttempts`), because the 50 per hour is one count over
everyone. Under the lock it
deletes attempts older than 24 hours, checks again that registration is
open, counts the limits, and saves the attempt and the link. The worker's
hourly [cleanup](#scheduled-cleanup) deletes expired links.

### Creating the account

`POST /api/v0/registration/links/redeem` takes `{"secret", "email",
"password"}` and needs no session. The password follows setup's rule, 15 to
128 characters, validated by `account.ValidatePassword`. The email is the one
the visitor types; it must be the link's, after `emailaddress.Parse`. Otherwise
someone could ask for a link to an address they control, send it to a
visitor, and have the visitor create that account, which its owner can
later take back with a password reset.

1. It reads the link without a lock. An unknown or expired link, or one
   sent to another email, is refused here, so a guessed secret costs no
   password hash.
2. It hashes the password with Argon2id, waiting for room in the hash
   budget (see [Password hashes](#password-hashes)), so one live link sent
   many times at once cannot start a hash per request.
3. In one transaction it locks the registration row, the system email row,
   the `users` table (the lock setup takes), and the link, then checks
   everything again.
4. It creates a member with `email_verified_at` set, since following the
   link proved the email, deletes every link to that email, and records
   `member-registered` with the new member as `target_user_id` and no
   actor.

It answers 201 `{"id", "email"}` and sets no cookie; the web app sends the
visitor to sign in. Other answers:

- 422 on `body.password` for a password outside the rule.
- 422 on `body.email` for an invalid email, or "Enter the email address
  this link was sent to." for another one. It names no address.
- 404 `registration-link-invalid`: unknown, used, or expired.
- 409 `registration-closed`: closed or paused. Pausing keeps links, so the
  link may work later; closing deleted it.
- 409 `registration-account-exists`: the email got an account first, e.g.
  through a second submission of the same link that waited for the first.

There is no attempt limit: a 256-bit secret cannot be guessed. Both
registration writes also pass the in-memory request limit every public
write has (see [Sign-in and browser sessions](#sign-in-and-browser-sessions)).

To list registration events:

```sql
SELECT id, occurred_at, action, actor_user_id, target_user_id
FROM audit_events
WHERE action IN ('registration-opened', 'registration-closed', 'member-registered')
ORDER BY id DESC;
```

## Password reset

A user who forgot their password follows a link sent to their verified
email and chooses a new one (`passwordreset`). ADR030 in the project's
decision records explains the choices.

### Links

`password_reset_links` keeps the SHA-256 hash of each link's
`randomsecret` secret, the user, the `email` it was sent to, and when it
was created and expires, one hour later. Asking again keeps earlier links;
completing a reset deletes all of the user's links, and so do deactivating
a member and `inbok recover-admin`. The hourly
[cleanup](#scheduled-cleanup) deletes expired links, and attempts that no
limit counts any more.

### Asking for a link

`POST /api/v0/password-reset/links` takes `{"email"}`, needs no session,
and answers 202 `{"status": "requested"}` whatever happens to that email.
Other answers:

- 422 on `body.email` for an invalid email.
- 429 `password-reset-throttled` with `Retry-After`: this client asked 10
  times in 15 minutes. It never depends on the email.
- 429 `too-many-requests`: the in-memory request limit every public write
  has (see [Sign-in and browser sessions](#sign-in-and-browser-sessions)).

The operation calls `passwordreset.Requests.Request` with the email and the
client's address, which answers the same for every address. Before it
returns, it only:

1. checks that the email is valid (`emailaddress.Parse`);
2. in one transaction, under a lock on the client, records an attempt in
   `password_reset_attempts`, looks up the ID of the account with the
   email, and saves the [reset link job](#password-reset-link) with both,
   unless the client already made 10 requests in the last 15 minutes. A
   client is an IPv4 address or an IPv6 /64 (`clientprefix`). Over the
   limit it returns an `*attemptlimit.Error` wrapping
   `passwordreset.ErrThrottled` with the wait.

Every request does the same work, whether or not an account has the
email, so the answer and its timing are nearly the same either way: an
indexed lookup that finds a row costs a few microseconds more than one
that does not, far below a network's jitter, and the client limit leaves
too few tries to average it out. Everything that differs runs in the job,
which `passwordreset.Links.Send` does. In one transaction it locks the
account, if any, and decides:

- no account, an unverified email, or a deactivated account: nothing is
  sent;
- 3 links sent to the account in the last hour, or 5 in 24 hours: nothing
  is sent. These limits are silent, so the answer cannot depend on them.
- otherwise: it saves a link for the account's stored email and counts
  it: it sets the attempt's `user_id` and `linked_at`, or, for a later link
  for that attempt, such as a retry's, or one whose attempt is gone, saves
  an attempt row of its own with no client, which the client's limit leaves
  out. The email limits count `linked_at`, the time the link was saved,
  which can come later than the request.

Then it mails the link to `<public URL>/reset-password#<secret>`. The secret
exists only in memory and the email; the message is `Private`, so a failed
send's log, and the job's error, leave out the server's reply. A failed
send keeps its link, and the job retries with a new one. No requested
email is kept: the attempt holds the client, and the account as `user_id`
only once it got a link, and the job's input holds IDs. The hourly [cleanup](#scheduled-cleanup) deletes
attempts whose request and link times are both over 24 hours old, an hour
after that.

### Completing a reset

`POST /api/v0/password-reset/links/redeem` takes `{"secret", "email",
"password"}` and needs no session. The email is the one the person types; it
must be the link's, after `emailaddress.Parse`, so a link someone sends for
an account they control cannot set its password for a person who types
their own address. It answers 200 `{"email"}`, the account's email, so
the sign-in form can be filled in, and sets no cookie. Other answers:

- 422 on `body.password` for a password outside the rule.
- 422 on `body.email` for an invalid email, or "Enter the email address
  this link was sent to." for another one.
- 404 `password-reset-link-invalid`: the link is unknown, used, or expired,
  or its account can no longer reset. One answer for every reason.
- 429 `too-many-requests`: the in-memory request limit.

There is no attempt limit: a 256-bit secret cannot be guessed.

The operation calls `passwordreset.Completion.Complete` with the link's
secret, the email typed, and the new password, which follows setup's rule,
15 to 128 characters (`account.ValidatePassword`).

1. It reads the link and its user without a lock. A missing or expired
   link, a deactivated user, a user whose email is no longer the link's,
   or an email given that is not the link's is refused here, so a guessed
   secret costs no password hash.
2. It hashes the password with Argon2id, waiting for room in the hash
   budget (see [Password hashes](#password-hashes)).
3. In one transaction it locks the user, the lock sign-in, deactivation,
   and recovery take, and checks everything again.
4. It saves the password, deletes the user's sessions, sign-in
   challenges, and reset links, deletes the sign-in attempts counted for
   the user's email, and records `password-reset` with the user as
   `target_user_id` and no actor. TOTP and backup codes stay, so the next
   sign-in still asks for a code.

A password outside the rule is `account.ErrPasswordInvalid`, an invalid
email `emailaddress.ErrInvalid`, and a live link given with another email
`passwordreset.ErrEmailMismatch`. Every other refusal, a malformed, unknown,
used, or expired link, a link whose account changed its email, or a
deactivated user, is the same `passwordreset.ErrLinkInvalid`.
On success it returns the user, so the caller can name the email to sign in
with, and creates no session.

With the reset, in its transaction, `Complete` saves the
[password changed notice](#password-changed-notice), which emails the user
that their password changed, naming the time; the answer does not wait for
it. A notice that fails to send does not undo the reset.

To list password resets, and recent requests:

```sql
SELECT id, occurred_at, target_user_id
FROM audit_events
WHERE action = 'password-reset'
ORDER BY id DESC;

SELECT id, created_at, client_prefix, user_id, linked_at
FROM password_reset_attempts
ORDER BY id DESC;
```

## Gmail accounts

A member connects Gmail accounts with Google's consent page
(`connectedaccount`), as ADR006 in the project's decision records
decides. Only an instance with a Google client offers it:
`INBOK_GOOGLE_CLIENT_ID` and `INBOK_GOOGLE_CLIENT_SECRET`, which the deploy
guide's [Connecting Gmail](../deploy/README.md#connecting-gmail) section
shows how to create. `serve` builds `connectedaccount.Connections` only
then; without it, `GET /api/v0/connected-accounts/providers` answers
`{"offered": []}` instead of `["gmail"]`, and the two starts below answer
409 `gmail-not-configured`. In development the [Google emulator](#google-emulator)
stands in for Google.

### Connected accounts

`connected_accounts` keeps each account's owner, `email`, Google's `sub`
(`oauth_subject`), and its refresh token, sealed with
`INBOK_ENCRYPTION_KEYS` and bound to the user and the account, as
[Encryption keys](#encryption-keys) describes. A member connects a Google
account once; two members may each connect it.

`GET /api/v0/connected-accounts` lists the member's own accounts, oldest
first: `id`, `provider` (`gmail`), `email`, `needsReconnectSince`, null
while Google accepts the token, `status`, `createdAt`, `name`, `color`,
and `sync`. `name` is what the member calls the account, null until they
set one with `PATCH /api/v0/connected-accounts/{id}`, which refuses more
than 64 characters as sent, or a line break or other control character
(422 on `body.name`), trims white space, and clears the name when nothing
is left. `color` is the account's mark in the web app, 1 to 8, given at
connect: the lowest the member's other accounts use least, so a
disconnected account's colour goes to the next and a ninth account
starts the eight again. Two connects of one member at once take their
colours in turn, under a lock on the member's colours.
`status` is `needs-reconnect` once Google refused the token,
`token-unreadable` when no configured key opens it, and `usable`
otherwise; `Service.List` opens each token for it, saving nothing, so
restoring a lost key makes an account usable again (see [Encryption
keys](#encryption-keys)). `sync` is where the account's
[sync](#synchronization) stands: `status`, `messagesSoFar` while running,
`lastSyncedAt`, `failure`, and `failedAt`. The handler composes
`connectedaccount`'s list with `mailsync`'s reports, since `mailsync`
depends on `connectedaccount`; an account disconnected between the two
reads is left out. `syncStopped` is true when no run of
`inbok.sync.minutely` succeeded in the last 5 minutes, which the server
asks DBOS (`dbos.Client.SyncScheduleSucceeded`): no worker runs, a worker
runs without the Google settings, or its runs fail. Google's `sub` stays
on the server. Another member's account is answered as not found
everywhere, 404 `connected-account-not-found`, like a missing one.

### Connecting

1. `POST /api/v0/connected-accounts/google` starts a new connection, and
   `POST /api/v0/connected-accounts/{id}/reconnect` one that replaces that
   account's token. Each saves a state in `connect_states` and answers
   `{"url"}`, Google's consent page, which the web app opens in the same
   tab. The state is single use, bound to the member and the session,
   expires in 10 minutes, and holds the PKCE verifier. A member may have 5
   unfinished starts: a sixth answers 429 `connect-throttled` with
   `Retry-After`, the wait until the oldest expires. A finished flow frees
   its place at once, so only abandoned ones count.
2. Google sends the browser to `GET /api/v0/connected-accounts/google/callback`
   with the state and a code, or an error. The operation is public to the
   access check and checks the session itself, so every outcome, a
   missing session included, is a 303 to the web app's
   `/connected-accounts?outcome=<outcome>`, never a Problem Details body.
   The session cookie arrives because it is `SameSite=Lax` and Google's
   redirect is a top-level GET, which `withCrossOrigin` lets through.
   Without a Google client, e.g. one removed while a member was at
   Google, it answers `failed` and leaves the state, which the flow never
   used, to expire.
3. `connectedaccount.Connections.Finish` deletes the state before any
   check, so it is used up whatever follows; trades the code with the
   verifier; checks the refresh token, the granted Gmail scope, and the
   ID token's verified email; calls Gmail's `users.getProfile`, whose
   mailbox must be the ID token's email; and saves the account, with its
   sync state and its first [sync](#synchronization), or replaces the reconnected
   account's token.

The outcomes, the contract's `ConnectOutcome`, are `connected` (a
reconnect included), `cancelled`, `gmail-not-granted`,
`email-not-verified`, `no-gmail` (Gmail's 400 `failedPrecondition`, "Mail
service not enabled", e.g. for a Workspace account without Gmail),
`already-connected`, `different-account` (a reconnect to another Google
account), `expired` (a state unknown, used, expired, or from another
session), and `failed`. A `failed` outcome logs `gmail connect failed`
with an error holding no code, token, or query string; the request log
records only the path. The callback operation's tag, `Redirects`, keeps
it out of the web app's generated client: a call from the page would use
up a state.

### Disconnecting

`DELETE /api/v0/connected-accounts/{id}` answers 200 with the deleted
account. In one transaction it deletes the account, and with it the
account's connect states and everything synchronized for it, moves the
refresh token to `account_removals`, and saves the
[account removal](#account-removal) job and the job that deletes the
account's stored content ([Content deletion](#content-deletion)).
Jobs already running for the
account stop when `Access.Token` finds it gone.

### Sending mail

A member sends a new message, or a reply, from one of their connected
accounts (`outgoingmessage`, US015 and US009). `outgoing_messages` keeps
each message under a random `uuid`: its sending account, whose owner owns
it, the account's address when it was saved (a reconnect may change the
account's), To, Cc, and Bcc, subject, plain text body, status, failure
reason, the ID inbok gave it (`inbok_message_id`, a Message-ID with its
angle brackets), a reply's threading headers (`in_reply_to`,
`internet_references`), Gmail's message ID once sent
(`provider_message_id`) and thread ID once sent or, for a reply, when
saved (`provider_thread_id`), and when it was created and sent. The recipient
lists are JSON arrays of `{"name", "address"}`, as synchronized mail keeps
addresses, with empty names for now. Like synchronized mail, it has no
owner column of its own: reads join `connected_accounts` and match its
`user_id`, so the owner is stored once, and a disconnect deletes the
account's messages with it.

`outgoingmessage.Service.Create` checks the content first:

- At most 100 recipients in each of To, Cc, and Bcc
  (`*outgoingmessage.TooManyRecipientsError` names the list), at least one
  across them, each through `emailaddress.ParseRecipient`; a refused one
  is a `*outgoingmessage.RecipientError` naming its list and index. A limit
  per list belongs to one field the form can mark, which a limit across
  three could not.
- A subject of at most 500 characters without a line break or other
  control character, and a body of at most 100,000 characters without NUL.
  Both may be empty.

The contract states the same limits (`maxItems`, `maxLength`), so the web
and Huma refuse most of it first; the service checks again for every caller.

It then saves in one transaction (`InsertOutgoingMessage`), in this order:

1. It locks the member's `users` row, as a Gmail connect does, so one
   member's sends are counted one at a time and a repeated key finds the
   first request's message committed. A deactivated member is
   `ErrDeactivated`.
2. A request's `Idempotency-Key` the member already saved a message with is
   `ErrKeyExists`, before the account or the limit, so a repeat is never
   counted or refused for the attempt it repeats. The service then reads
   that message: the same account and content return it, anything else is
   `ErrKeyReused`. Keys are the member's own, in `outgoing_message_keys`
   (member, key, message): another member's request with the same key is
   another request. A key whose message a disconnect deleted meanwhile is
   free again, and the request is saved as a new one.
3. It reads the account under a key share lock, so a disconnect waits and
   then deletes the message too. Another member's account, or a missing
   one, is `ErrAccountNotFound`, one Google refused is
   `ErrNeedsReconnect`, and one whose token no configured key opens is
   `ErrTokenUnreadable` (`connectedaccount.Service.CheckToken`). Both answer
   409 `sending-account-needs-reconnect`: either way the member reconnects.
4. The member's send limit, 30 per 15 minutes and 200 per 24 hours across
   their accounts, counts `send_attempts` rows (the member and a time, no
   content). They are kept apart from `outgoing_messages`, whose rows go
   with their account, so a disconnect does not reset the count; a send
   deletes the member's rows older than 24 hours. Over the limit is an
   `*attemptlimit.Error` wrapping `ErrThrottled`.

The message gets an ID on the account's domain and is saved as `waiting`
with its attempt, its [send job](#sending-a-message), and its key, whose
primary key also refuses a second message for one key if a request ever
got past the member's lock. A message moves through `waiting`, `sending`,
then `sent`, `failed` with a reason (`refused`, `sending-limit`,
`needs-reconnect`, `token-unreadable`, `deactivated`, `sender-changed`, or
`gave-up`), or `unknown` when a send may have reached Gmail and no answer
or check says whether.

`Service.SendAgain` copies a `failed` or `unknown` message
(`InsertOutgoingMessageCopy`): the same content from the same account, with
the account's address now and a new ID and Message-ID, waiting with its own
job and counted as an attempt. No request named the copy, so it has no key.
The original's `sent_again_as` names the copy, so sending it again a second
time returns that copy, and the member never sends one message twice by
clicking twice. The transaction locks the member, then the account, then
the message, the order the send job and a disconnect use. `Service.List`
returns the member's newest 50, without bodies, so a `failed` or `unknown`
message, which synchronized mail may never show, can be found and sent
again.

`Service.Reply` saves a reply to one of the member's synchronized
messages, from any of their accounts, through the same steps; the request
names the answered message instead of a subject. Step 4 of the
transaction reads that message, scoped to the member's accounts
(`ErrAnsweredNotFound` otherwise), after the key's check, so a repeat
returns its reply though a sync deleted the message since. The reply
takes the message's subject, with `Re: ` before it when it has none and
the 500-character limit leaves room, so a subject within the limit stays
whole (a longer one is cut, and a control character becomes a space);
In-Reply-To and References by RFC 5322
section 3.6.4 (`msgid.Writable` drops a msg-id outside RFC 5322's current
syntax, and `msgid.TrimReferences` keeps 50); and its
`provider_thread_id` when sent from the account that holds it, since a
thread ID belongs to one mailbox: a reply from another account starts a
new thread in that account's Gmail. These
are copied onto the row, which keeps no link to `messages`: a sync may
delete the answered message, and the reply needs nothing more of it.
Saving a status without a thread keeps the one saved, and send again
copies all three. The key's row keeps the answered message's ID
(`answered_message_id`), so a key reused for another message, or for a
new message, is `ErrKeyReused`.

The HTTP operations, tag Outgoing Messages, are Send Message
(`POST /outgoing-messages`, which requires an `Idempotency-Key` header: a
UUID, quoted as the IETF draft's Structured Field string or bare), Reply
to Message (`POST /messages/{id}/replies`, the same header and body
without `subject`; a message the member does not have answers 404
`message-not-found`), List Outgoing Messages, Get Outgoing Message, and
Send Message Again (`POST /outgoing-messages/{id}/send-again`). A refused account, missing
recipients, and a refused address answer 422 on their field, e.g.
`body.cc[2]`; a reused key answers 422 `idempotency-key-reused`. Over the
send limit answers 429 `send-throttled` with `Retry-After`. Another
member's message, or a missing one, answers 404
`outgoing-message-not-found`, and sending again a message that is
`waiting`, `sending`, or `sent` answers 409 `not-sendable-again`.

The send builds the MIME message from the saved row with go-message:
`From` the saved address, `To`, `Cc`, `Bcc`, an RFC 2047 `Subject`, the
creation time in UTC as `Date`, the saved ID as both `Message-ID` and
`X-Inbok-Message-Id`, a reply's `In-Reply-To` and `References`, and
quoted-printable UTF-8 `text/plain`. Gmail
replaces the `Message-ID` with its own, which synchronized mail keeps as
`internet_message_id`; providers that keep a client's send the saved one.
A subject holding `=?` is encoded whole, so a reader shows it as typed
rather than decoding it. The same row gives the same bytes on every
worker. Building checks again each value that becomes a header (the
recipients, `From`, the subject, the msg-ids), so none can add one, whatever
wrote the row; it does not apply the limits
again, so lowering one leaves saved messages sendable. ADR006 records these
choices.

### Synchronized mail

`mailsync` keeps a copy of each connected account's recent mail metadata
and mailboxes, as ADR001 in the project's decision records decides; the
provider stays authoritative. ADR001's "Synchronization rules" lists
what synchronization does and where each rule comes from. A
[sync](#synchronization) copies an account when it is connected, and
polls keep the copy current. The
model follows JMAP Mail (RFC 8621), which covers both folders and Gmail
labels, so the features that read mail never see Gmail's own encoding.
Four tables hold it, each deleted with its account:

- `mailboxes`: an account's Gmail labels, or later its folders, with the
  provider's ID, its own name and the provider's ID of the mailbox it
  nests under (`provider_parent_id`; the Gmail adapter stores a label
  `Work/Projects` as `Projects` under `Work`), and a `role` for the
  special ones, NULL for the member's own. The roles are the purposes in
  IANA's IMAP Mailbox Name Attributes registry, as JMAP names them, e.g.
  `inbox`, `sent`, `junk`, `trash`, or `snoozed`. ADR007 lists how
  Gmail's labels map: `UNREAD` and `STARRED` are flags, not mailboxes.
  `made_by` says who made it: the `provider`, for Gmail's labels of type
  `system`, e.g. `INBOX` or `CATEGORY_SOCIAL`, or the `member`; the
  labels a member sees have no role and are the member's.
- `messages`: one row per provider message, with inbok's own random `id`
  (a UUID, never derived from mail), Gmail's message and thread IDs, the
  provider's own timestamp (`internal_date`, Gmail's `internalDate`), the
  subject, the From, Reply-To, To, and Cc addresses as JSON lists of
  `name` and `address`, the provider's `preview` (Gmail's snippet), `seen`
  and `flagged`, and the `Message-ID`, `In-Reply-To`, and `References`
  headers a reply threads with. No body: content storage keeps that. An
  account has each Gmail message once, so a repeated step updates the
  row, which keeps its `id`.
- `message_mailboxes`: which mailboxes each message is in, perhaps none.
  An archived Gmail message is in no inbox but keeps its other labels.
  Each row names both sides with the account, through foreign keys on
  (account, ID), so PostgreSQL refuses a membership that joins two
  accounts' rows. Deleting a message or a mailbox deletes its rows.
- `sync_states`: one row per account, saved with it, with the full sync's
  `status` (`waiting`, `running`, `complete`, or `failed`), its
  `sync_generation`, the `position` the next poll asks for changes since,
  when inbok last confirmed the copy (`last_synced_at`), and when the
  member last asked to sync now (`sync_requested_at`), which only that
  request writes, for its limit and a poll's recheck. `failure` and
  `failed_at` say why and when the last sync, full or poll, failed, until
  one succeeds: `provider`, `token-unreadable`, or `internal`. A `failed`
  full sync always has one, and a `complete` account keeps a failed
  poll's. The position is text only the provider's code reads, e.g.
  Gmail's history ID in decimal, so storage neither parses nor checks it.
  `mailboxes` and `messages` reference this row rather than the account's,
  so a disconnect deletes the account, then the sync state, then the mail,
  the order a sync write locks them in: the two never deadlock.

The migration lists, as comments, the columns IMAP and Outlook will need,
such as a folder's UID validity and a message's UID in each folder.

Every write goes through one method, `UpdateSyncedMail`: it locks the
account's sync state, passes it to a function that returns a
`mailsync.Change`, and writes the change in one transaction: the
mailboxes, deleted messages, saved messages with their mailboxes,
updated messages' mailboxes and flags, the sweep of earlier generations,
the conversations of every thread these touched, the
[content deletion](#content-deletion) of what the deleted messages had
stored, and the new state. An update for a message the account does not
have is skipped. A change that saves one message twice keeps the last,
and one that deletes and saves a message only saves it, so the message
keeps its `id` however often a step repeats. A saved or updated message
naming a
mailbox the account does not have, e.g. a Gmail label made since the
labels were listed, writes nothing and returns
`mailsync.ErrUnknownMailbox`; so does any other error, e.g. a generation
or count past the `integer` columns. A feature that must act on the same
writes adds its job to that transaction, as the deletion of deleted
messages' content and the [search index](#search-index)'s job do.
Each full sync starts a sync generation, and each message records the
generation that last saved it, so a full sync that finishes can delete
the messages it did not see, a batch per change, without comparing
clocks. Storage keeps one account's writes in order; checking that a
step's generation is still the current one is the full sync's job.

Writes can still land out of the order Gmail made them: an action's
answer may be written after a poll has brought in a newer change to the
same message. So each message keeps `provider_version`, the provider's
change number its state was read at, which grows with every change:
Gmail's `historyId` of a fetched message or an action's answer, or the
ID of the history record a poll's update comes from. An update read at
an older version than the stored one is skipped, its mailboxes and
conversation too, so a late write never undoes a newer one. A save is
written whatever its version: a full sync's stale save is followed by a
poll from before it, which brings the newer state back. A message or
update without a version, or past a `bigint`, writes nothing.

`mailsync.Provider` is what synchronization reads from the provider, and
the `google` client implements it for Gmail. Every method only reads and
takes an access token from [Calling Gmail](#calling-gmail):

- `Mailboxes` lists the labels as mailboxes, `Position` returns the
  account's current history ID (`users.getProfile`), and `ListMessages`
  returns one page of 100 message IDs received after a time, in whole
  seconds, newest first, with or without Spam and Trash.
- `Messages` fetches metadata (`messages.get` in `metadata` format) for
  a page of IDs. Gmail's rules stay in `google`: `UNREAD` and `STARRED`
  become `seen` and `flagged`, the other labels mailboxes, and the
  snippet loses Gmail's HTML escaping. Headers are decoded with
  go-message: RFC 2047 encoded words in any charset it knows, quoted
  display names included, and address lists with groups. A list that
  does not parse is mended as enmime does (white space inside `<...>`, a
  missing comma), then parsed entry by entry, an entry that is no
  address kept as its text. Msg-ids are read leniently, past phrases and
  comments and across a fold, keeping their angle brackets. A message
  Gmail no longer has comes back as gone.
- `Changes` reads one page of `history.list` since a position and
  sorts each message it names into one list: deleted, whatever else
  happened to it; added, to fetch whole; relabelled, an update with the
  whole label set its last label record carries, `UNREAD` and `STARRED`
  as `seen` and `flagged`, at the record's ID as its version; or reread,
  when that record lists no labels, since Gmail leaves out an empty list
  too. Each label record's message
  lists its labels just after the record (observed on real Gmail; ADR001
  "Reading Gmail"). Gmail's 404 for a history ID it no
  longer keeps returns `mailsync.ErrPositionExpired`, which means a full
  sync; so does a position that is not a history ID, without asking
  Gmail.

What a sender writes is bounded: each Gmail answer at 4 MiB, but a
message's content at 16 MiB in full format and 64 MiB for an attachment,
and each header at 16 KiB, cut on a whole character before it is parsed;
a longer `References` is read only at its first and last 8 KiB. Of what
parses, each address list keeps 500 entries and `References` 50 msg-ids,
the first and the last 49. Invalid UTF-8 and NUL, which PostgreSQL's
`text` and `jsonb` refuse, become U+FFFD in every stored string. A
message whose headers pass 4 MiB is fetched again without them. None of
these can make a message fail to save, which would stop the account's
sync.

Gmail allows 6,000 quota units a minute per user; a metadata fetch costs
20, a full one 60, and an attachment 20. An account's `Messages`,
`Message`, and `PartContent` calls spend at most four fifths of that
across calls: four metadata fetches a second, with at most four of a
call in flight, or four full ones every three seconds. That leaves room
for the same account's sends and polls, and for the messages a member
opens. The pace is the process's own; one sync and one body fetch per
account at a time keep the worker's whole, while another process's
fetches, e.g. for a member who opens a message, use that process's own,
inside the room left. A call whose wait for the pace would outlast its
context's deadline returns `mailsync.ErrProviderBusy` at once, sending
nothing. Each `Provider` call retries a 429, a 403 rate limit, a 500,
502, 503, or 504, or a timeout after 1, 2, 4, and 8 seconds, each plus
up to a second at random, or after the `Retry-After` Gmail sends; past
five attempts, or for a `Retry-After` over 32 seconds, it returns
`mailsync.ErrProviderBusy` for the job to try later. Errors keep only
Gmail's status, never its text, and an answer that does not decode fails
without quoting it. The Google client's own logging is off, since at
`GOOGLE_SDK_GO_LOGGING_LEVEL=debug` it would log whole answers.

### Conversations

The mail lists show conversations: an account's messages that share
Gmail's thread ID (ADR031). Two tables keep them, derived from the
synchronized mail so a list reads one index and never groups messages
(ADR001, "Conversations"):

- `conversations`: one row per thread with a message to show, with
  inbok's own random `id`, which the web app's URLs and the other mail
  features use, and every column a list row shows: the newest message's
  date (`latest_date`), preview, and From, the oldest message's subject as
  sent, up to 10 senders in date order, the message count, and whether a
  message is unread or flagged. The row is made from the thread's
  visible messages, those in neither Spam nor Trash and no drafts; a
  thread with none is made from its Spam and Trash messages, and a thread
  of drafts alone has no row. `in_all_mail` says it has a visible
  message.
- `conversation_mailboxes`: the mailboxes each conversation is in: those
  holding a visible message of it, and Spam and Trash when they hold any.
  Each row repeats the conversation's date and unread, so one index
  serves a mailbox's list and its unread count.

The rules are one Go function, `conversation.Derive`, which turns a
thread's messages, each with its mailboxes' roles, into the row and the
mailboxes, or into no row for a thread of drafts alone; its tests need no
database. Both tables are written only by `deriveConversations` in
`repository/postgres`, which `UpdateSyncedMail` runs for the threads its
change touched, after every other write of the change: it reads those
threads' messages, derives each, deletes the rows of threads left with
nothing to show, and saves the rest, which keep their `id`. Every write to the mail tables goes
through `UpdateSyncedMail`, which keeps the two right. SQL run by hand,
or a write to `messages`, `message_mailboxes`, or `mailboxes` added
anywhere else, leaves them stale: such a write must go through
`UpdateSyncedMail` or derive the threads it touched. So does a change to
how a row is derived, which leaves existing rows as they were until
their thread is written again. ADR001 lists the fixes considered, and
`DEBT.md` holds the planned one: a rules version that `inbok migrate`
derives every thread again on.

`conversation` reads them for the member, all under `/api/v0` and the
Conversations tag. Each read first checks that the place's label, the
account filter, or the conversation is the member's, then reads with the
IDs it found, in one read-only snapshot:

- `GET /conversations?place=&account=&before=&beforeId=&limit=` lists a
  page, 50 by default and at most 100, newest first, then by `id`
  descending. `place` is a view, `inbox`, `starred`, `sent`, `all`
  (every visible conversation), `spam`, or `trash`, or the mailbox ID of
  one of the member's labels (no role, made by the member); `account`
  limits it to one account. A next page passes the last
  row's `date` and `id` as `before` and `beforeId`; asking for 51 tells
  whether one follows. Each page reads each mailbox's or account's index
  and merges them. Each row carries the place flags `inbox`, `sent`,
  `allMail`, `spam`, and `trash`, `starred`, and `labelIds`, so a client
  can tell which lists hold it; Starred is `starred` and `allMail`.
- `GET /conversations/new-count?place=&account=&after=` counts a place's
  conversations dated after `after`, the newest one a list shows.
- `GET /places` lists each account's six views and its labels, with
  each one's unread conversations, in a stable order; the web app orders
  them for display.
- `GET /conversations/{id}` returns a conversation's row and its
  messages but drafts, oldest first, with their headers, read and starred
  state, labels, whether each is in Spam, Trash, or Sent (`sent`: its
  account sent it, from its own address or an alias), the message it
  answers (`answers`), and whether it has attachments (`attachments`); no
  body. The repository returns every message with its mailboxes, its
  Message-ID and In-Reply-To, and its stored parts, read in one more
  query; `conversation`'s rules leave out the drafts, mark Spam, Trash,
  and Sent, and link each message to the one its In-Reply-To names last
  (`linkAnswers`). A message has attachments when its view offers a
  download, as `messagecontent.Choose` decides for the message view, so
  the two agree; one whose content was never fetched has none yet. Its
  `providerUrl` is where the provider's own web app shows it, or null:
  the service asks a `conversation.Linker`, and `google.WebApp` builds
  Gmail's undocumented address from the account's address and the
  thread, in Spam or Trash when the conversation is only there.

A label of another member answers 404 `place-not-found`, an account
filter of another member 404 `connected-account-not-found`, and another
member's conversation 404 `conversation-not-found`. On 20,000 synthetic
conversations over three accounts (PostgreSQL 18, 2026-11-11) a page
reads in about 3 ms, deep pages included, and every unread count in
about 11 ms.

`POST /conversations/actions {action, ids}` changes up to 100 of the
member's conversations, by Gmail's own rules: `read` and `unread`,
`archive` and `unarchive`, `trash` and `untrash`, `star` and `unstar`,
`spam` and `unspam`. `star` and `unread` change a conversation's
newest message (`Action.OnNewest`): the newest message its row is made
from that is not in Sent, else the newest, which `Derive` records as
`newest_message_id`; the rest change every message. `untrash` puts a
conversation back in the inbox, as Gmail's Move to Inbox does, since
Trash took it out. `conversation.ActionService` calls Gmail inside the
request, and inbok writes only what Gmail answers (ADR031, "Mail actions
as built"):

1. It finds the member's threads, another ID refused `not-found`, and
   reads their rows as they stand before.
2. Per account, at once, it takes an access token and reads the
   account's version (`Version`, Gmail's history ID from
   `users.getProfile`), then calls `threads.modify` or `threads.trash`
   for each thread, or `messages.modify` for its newest message, four at
   a time per request, for at most 7 seconds and never into the last 3
   of the request's 10, which the writes and answer keep.
   `google.Client.ChangeThread` holds Gmail's rules, which action is
   which label, and returns each message's labels as Gmail answered.
   Gmail answers without history IDs, so each update is written at the
   version read before the calls: below any change made after them,
   which the next poll then writes over it.
3. It writes those labels through `UpdateSyncedMail`
   (`UpdateMessages`), which derives the conversations again and
   keeps the sync state; a label made since the last sync is left out
   until the next poll lists it, and a message a poll has meanwhile
   brought to a newer version keeps it.
4. It answers with the changed rows, as read after the writes, each
   refused conversation with its reason (`not-found`,
   `needs-reconnect`, or `failed`), `undo`, and `places`, every unread
   count as `GET /places` answers, left out and logged when counting
   fails. `undo` is the changes that put each changed row back as it
   was: the difference between its row before and after
   (`conversation.undoOf`), within what the action reaches
   (`Action.Reach`): an action that moves a conversation, its places
   (the inbox, Spam, Trash, its labels); one that marks it, that star
   or unread mark. So a mark changed meanwhile, e.g. a new reply's
   unread, or one the row reads differently once its messages moved, is
   not taken back. Conversations needing the same change go together.
   By the row, not each message: undoing Trash also puts the member's
   own replies in the inbox, as one `threads.modify` labels every
   message, and a thread only partly in Trash or Spam that the action
   put there whole comes back out of it whole, the message deleted on
   its own included (DEBT.md, "Undo per message").

`POST /conversations/changes {changes}` applies changes as they are, by
none of Gmail's rules, an action's `undo` among them: each adds its
conversations to places and takes them out of others (the inbox, Spam,
Trash, or the member's labels), stars or unstars them, or marks them
unread or read, as the actions do, up to 100 conversations, each in one
change. It runs as an action does, with the same answer, its own `undo`
included, which reaches what each change names. The service turns each place into the conversation's
account's mailbox (`ListPlaceMailboxes`), and refuses the whole request
with 404 `place-not-found` for a label that is not in it, e.g. deleted
since. `google.Client.ModifyThread` makes one `threads.modify` for the
labels and the marks it clears, then one `messages.modify` for the
newest message's marks it sets; when that second call fails, the first
call's answer is written and the conversation counts as changed, its
`undo` reaching only that part. Gmail takes at most 100 labels to add
and 100 to remove per call, so a change adds and removes at most 100
places each, and an `undo`, which swaps them, is a change the operation
takes too; when the places to remove leave no room for the star and
unread marks it clears, `ModifyThread` clears those in a second
`threads.modify`, and when that one fails, the first's answer is written
as above. A provider whose messages sit in one
folder each moves them: such a change removes one place and adds one.

Each conversation is changed or refused on its own, so one answer can
hold both, and nothing changes in inbok that Gmail did not accept: a
refusal needs no undoing. The poll that follows finds Gmail's record of
the action, which inbok already holds. Opening a conversation marks
nothing read; the web app sends `read`. Without a Google client the
operation answers 409 `gmail-not-configured`, and the API server takes
access tokens itself, as the worker does.

## Search index

Search finds a member's messages through Meilisearch, a derived index: a
searchable copy of message data that returns message IDs, with where the
query matched and an excerpt of the body around it, while the messages
themselves load from PostgreSQL with the usual ownership check.
ADR005 in the project's decision records explains the choices. Jobs
keep the index in step with synchronized mail, and Search Messages
[searches](#searching) it.

The index holds every message inbok keeps, Spam and Trash included, and
a query decides which mailboxes it leaves out. Each write to a member's
mail saves, in its own transaction, a job naming the messages it
changed, so the job commits with the change and a rollback drops it:

| Write | Job |
|-|-|
| `UpdateSyncedMail`: messages saved, updated (labels and flags), or deleted, by a sync or a member's action | `inbok.search-indexing` for those messages and every message of their threads |
| `SaveMessageParts`: a body stored | `inbok.search-indexing` for the message |
| `SavePartBlob`: a text body the provider sends apart, e.g. Gmail's long HTML | `inbok.search-indexing` for the message |
| `DeleteConnectedAccount`: an account disconnected | `inbok.search-account-removal` for the account |

A job's input is the member and the message IDs, never mail. When it
runs, `search.Indexer` reads those messages as PostgreSQL holds them
then, upserts the ones it finds, and deletes the ones it does not, e.g.
deleted since. Both jobs run on the queue `inbok.search`, partitioned by
member with one run at a time in each partition
(`dbos.WithPartitionConcurrency(1)`), so a member's runs never overlap
while different members' run side by side. Together these keep the
index right whatever order runs take: Meilisearch applies writes in the
order they reach it, but a retried job could send an older state after a
newer delete; instead the run that comes last reads, and writes, the
latest state, so an older job retried after a newer delete deletes again
rather than bringing the message back, and a rebuild, below, runs in the
member's partition too. That limit is what keeps the index correct, not a load setting, so every worker start
restores it, unlike the other queues' limits.

A run works through its messages 100 at a time (`search.BatchSize`),
one DBOS step each, and each step retries for about an hour (1, 2, 4,
8, then 15 minutes three times; a worker restarted meanwhile starts the
count again) while the member's later runs wait behind it. A run that still fails is logged and dropped: its messages
stay as they were in the index until they change again or the index is
[rebuilt](#rebuilding-the-index), e.g. by the next nightly rebuild. A message's document holds its subject, sender, recipients,
date, account, the inbok IDs of its mailboxes, its conversation's
places, and, once its body is stored, its text in two fields: `body_text`, the text of the bodies a
view of it shows, and `alt_text`, the text of its other text parts,
e.g. the plain-text alternative of HTML a view shows. A word in either
finds the message, and one in `body_text` ranks it higher. Until its
body is stored, a message is found by its headers. The document's
`conversation` field, the account's ID and the provider's thread ID,
names the conversation a search keeps one message of. Its conversation's
places are `conversation_mailbox_ids`, the mailboxes the conversation is
in as lists place it (`conversation_mailboxes`), and
`conversation_flagged`, whether it is starred: a reply in the inbox puts
its whole conversation in the inbox, so the derive step of
`UpdateSyncedMail` returns every message of each thread it derives, and
the write's job indexes them all. A long thread's new reply so indexes
its older messages again, which ADR005 accepts. Three more fields serve
filters: `from_addresses`, every address of its From as PostgreSQL holds
it, without empty ones, e.g. a group's name; `internal_date`, when the
provider received it, in Unix milliseconds, Gmail's own precision; and,
once its body is stored, `has_attachment`, whether a view offers any of
its parts as a download, as `messagecontent.Choose` picks them from the
stored part tree. Every message changed is read again, so marking one
read re-reads its stored body text too.

- `search` declares the `Index` interface: `Prepare`, `Upsert`, `Delete`,
  `DeleteAccount`, `Search`, and, for a rebuild, `Owners`,
  `DocumentIDs`, and `DeleteOwner`, with its `Document`, `Query`, and
  `Result` types; a result's `Hit`s carry where the query matched, in
  bytes. Each method may be called again with the same input,
  e.g. by a job that retries, and returns once Meilisearch has applied
  the change, so the next search sees it.
- `Search`, `Delete`, and `DeleteAccount` take the owner as an argument
  of their own and refuse an owner of 0, and each touches only that
  member's documents, whatever IDs it is given: no search returns
  another member's message, even for another member's account ID. The
  caller passes the signed-in member, since no index can tell a wrong
  member from the right one. A query is structured: words, accounts,
  mailboxes to leave out, senders, a received-date range, whether it has
  an attachment, and a page; callers never write Meilisearch's filters.
  The adapter quotes each value whole inside its own expression.
- `meilisearch` implements `Index` with the official Go client,
  `meilisearch-go`, with one Meilisearch index per member, named after
  the member's user ID, e.g. `messages_42`. Every upsert applies the
  settings before its documents, so a member's index has them however it
  came to exist, and their first documents never sit in an index without
  them, which would refuse every search's owner filter; until a member's
  first upsert, their searches find nothing and their deletes do nothing.
  Searches and account deletes also filter on the owner inside the
  member's index. Its errors carry Meilisearch's error code, never the
  text of its reply, which can quote a message. An error from a call cut
  short by its context wraps the context's error, so a request's
  deadline still becomes a 503.
- `Prepare` applies the settings to every member's index, and every
  upsert to its member's: which fields a search matches, in order of
  weight, and which it filters on, only those today's searches use: a
  filter added later adds its field, and Meilisearch reindexes from the
  documents it holds. The ranking rules are Meilisearch's defaults and
  then the date, newest first, so equal matches keep one order however
  the index was written; typo tolerance is the default but for numbers,
  so order 4471829 never finds 4471828. An update leaves the settings it
  omits as the index has them, which on a new index are Meilisearch's
  defaults. Applying the same settings again, or from two processes at
  once, changes nothing. The worker calls `Prepare` when it starts,
  before any job runs and before it reports healthy. Since every write
  applies its worker's settings too, an old worker still running would
  undo a new release's, so `deploy/upgrade.sh` replaces the worker before
  the server, and a rollback the server before the worker: no server
  filters on a field the index does not allow.
- `messagecontent.Texts` reads a message's stored text parts from
  content storage, apart by whether a view shows them: the bodies
  `messagecontent.Choose` picks, in that order, and every other text
  part in the message's order, e.g. the alternatives a view does not
  pick. It reads at most 64 parts and 1 MiB over both, the shown ones
  first. It also says whether a view offers any part as a download, from
  the whole part tree, whatever those limits leave: a PDF or a photo a
  phone sends beside the text counts, a logo the HTML shows does not. An
  image in the HTML's `multipart/related` that the HTML never refers to,
  which the reading pane also offers, does not count: telling it apart
  takes the sanitized HTML.
- `search.BodyText` turns each group into a field's text: plain text as
  it is, and the text of HTML that a reader sees, as the HTML Standard's
  default rendering decides it, read token by token and never parsed into
  a tree with `html.Parse` (ADR005 says why), in order, cut at a word to
  at most 64 KiB. The shown text becomes `BodyText`, the rest `AltText`,
  so a stub such as "View in browser" never stands in for HTML a reader
  sees, and the words of an HTML message that is only images are still
  found in its plain-text alternative.
- `search.Indexer` is what the jobs call: `Index` brings a member's
  messages up to date from `IndexStorage`, which `repository/postgres`
  implements, and `RemoveAccount` removes a disconnected account.
- `search.Rebuild` is what a rebuild calls, below: `Owners` lists whom it
  covers from `RebuildStorage`, which `repository/postgres` implements,
  and the index, and `Owner` rebuilds one member through `Indexer`.
- `searchtest` holds `Fake`, an `Index` in memory for tests of code
  that uses one, and `RunContract`, the tests both `Fake` and
  `meilisearch` pass, so the fake behaves as Meilisearch does apart from
  text matching. Other features' fakes live unexported in the feature's
  own tests; this one is shared and held to the contract, because a fake
  that kept members apart less strictly than Meilisearch could let a
  test pass while mail leaked between members. `meilisearch`'s tests run
  against the image the Compose files use, through Testcontainers, like
  the PostgreSQL tests.

### Searching

Search Messages (`GET /api/v0/messages/search`) calls
`search.Searcher`, which reads through `SearchStorage`, which
`repository/postgres` implements:

1. A search of one account checks it is the member's, so a search of
   one they no longer have answers 404 rather than finding nothing.
2. It looks up the IDs of the member's mailboxes with the roles junk,
   trash, and drafts, and searches the index without the messages in
   them: Spam and Trash as Gmail leaves them out, and drafts, which no
   view shows.
3. It reads the messages found from PostgreSQL by their IDs, keeping the
   index's order and dropping those in such a mailbox now, deleted, or
   another member's. The index may lag behind, so PostgreSQL decides
   what is shown: at worst a page holds fewer results than its limit,
   and the total is off, until the index catches up.

The index returns one message per conversation, its best match
(Meilisearch's search-time `distinct` on `conversation`), so the total
and the pages count conversations. Every word of the query must match
(`matchingStrategy: all`), as in Gmail, so adding a word narrows a
search; Meilisearch reads only a query's first 10 words. Each hit
carries its subject and an excerpt of its body text around the best
match, which Meilisearch crops to about 20 words, both with every match
marked by STX and ETX, two control characters `search.BodyText` drops
from body text, so they never stand for its text (a subject holding one
loses its marks); the adapter takes the
marks out and keeps where they were, in UTF-8 bytes. It never asks for
Meilisearch's `_matchesPosition`, which lists every match in both body
texts: a crafted mailbox made one page of 100 results decode to about
500 MB. Meilisearch matches a query's last word as a prefix too and
marks as many of its characters as the query word has, so the adapter
completes a mark to its word's end when at most two letters are left,
Meilisearch's most typos per word: a typo, `anggran`, marks the whole
of "Anggaran", while a prefix, `inv`, stays as typed in "Invoice". The
searcher keeps a subject's matches only when the index's subject is
PostgreSQL's, drops any match that leaves its text or splits a
character, and starts an excerpt at most three words before its first
match, so the match shows on a phone's one line. A message whose body
text did not match shows the provider's preview, with nothing marked.
The handler turns byte positions into UTF-16 code units, which is what
a JavaScript string counts. A Meilisearch that does not answer, or
answers that it failed, is a 503 `search-unavailable` with
`Retry-After: 15`.

Filters narrow a search, each a structured parameter the web builds,
never syntax parsed out of `q`: `from`, repeated for up to 10 email
addresses (`idn-email`, as every address field is), keeps a message whose
From holds any of them, compared as Meilisearch's filters compare text,
ignoring case (so `BUDI@x.com` finds `budi@x.com`);
`since` and `before` keep what the provider received at or after one
instant and before another, each rounded up to the index's millisecond,
which is exact, and a `since` not before `before` is a 422 on
`query.before`, checked before the search takes a running place;
`has=attachment` keeps a message with `has_attachment`. A `from` that is
no address anywhere in the list, and an instant given that equals Go's
zero time,
are 422s too: Huma decides whether a parameter is there from its first
value alone, so the input's `Resolve` refuses a list whose first sender
is empty, which Huma would skip with all its checks.
Meilisearch applies filters before it keeps one message per
conversation, so each filter holds for the message a row shows: a
conversation whose words are in one message and whose file is in another
is not found, as in Gmail. Without `q`, every message the filters keep
matches, newest first by the date ranking rule, each conversation
through its newest such message, and the adapter asks for IDs alone,
since there is nothing to mark. `has=attachment` only finds messages
whose content inbok has fetched: not those of an account connected
moments ago, while the body fetch catches up; unopened ones in Spam or
Trash, which it skips; one whose fetch failed for good; nor one that
reaches inbok older than 90 days, e.g. imported, or ages past 90 days
before its turn, which the body fetch never takes unless it is opened.

Cutting excerpts is most of a search's cost in Meilisearch, and it grows
with each result's body text: on 100 crafted 64 KiB messages, a page of
100 took 0.76 s against 4 ms for IDs alone. So a page holds at most 50
results, and a member may have two searches running at once
(`runningSearches`, counted in serve's memory); a third answers 429
`searches-running` with `Retry-After: 1` before it reaches
Meilisearch. A search runs apart from its request and gives its place
back only when it ends, within 15 seconds, so a client that gives up and
asks again cannot run more; Meilisearch may go on past that, but serve
stops waiting. Each serve process counts on its own.

### Rebuilding the index

Jobs change only the messages that change, so a copy that was lost or
went wrong stays so without a rebuild: a new, empty Meilisearch after
an upgrade, documents an earlier release wrote, e.g. before a field or
the body-text cap changed, a database restored from a backup, or the
runs of a job that gave up. A rebuild
makes each member's index what PostgreSQL and stored text hold, in
place:

1. `Rebuild.Owners` lists every member in PostgreSQL, with mail or not,
   and every owner Meilisearch keeps an index for.
2. For each, in the member's partition of `inbok.search`, so live jobs
   wait while it runs, `Rebuild.Owner` deletes the whole index of a
   member PostgreSQL no longer has. Otherwise it lists the IDs the
   index holds, writes each of the member's messages 100 at a time with
   `Indexer.Index`, then passes the listed IDs it did not write to
   `Indexer.Index` too, which deletes them, or writes one that came back.

It never empties an index first, so searches keep finding what the index
held while it runs. A member's rebuild is one DBOS step, not one per
batch: DBOS skips a recorded step when a run resumes, though the index
it wrote to may have been replaced since, e.g. by a new, empty
Meilisearch. An interrupted step is not recorded, so a resumed run
repeats the member whole; an index replaced after a member's step
finished needs a new rebuild, which `upgrade.sh` and the restore steps
run. A rebuild of every member, `inbok.search-rebuild`, enqueues
the members' runs, `inbok.search-member-rebuild`, and waits for them;
it runs outside every member's partition, since it waits on them. It
runs nightly at 03:00 UTC, when the worker is running then (DBOS does
not make up a missed night), as `inbok.search-rebuild-schedule`
from the schedule `inbok.search-rebuild.nightly`, and when the command
asks for it, from the queue `inbok.search-rebuild`:

```sh
set -a; . ./.env; set +a
go -C backend run ./cmd/inbok search rebuild
```

The command needs only `INBOK_DATABASE_URL` and a running worker, which
does the work; it waits up to two hours and prints what the rebuild did.
`deploy/upgrade.sh` runs it after every upgrade, and the deploy README's
restore steps run it before the site opens again. A database that held
mail before search was indexed needs one run.

Remove a field from documents in two releases, as with a column: first
stop searching, filtering, and sorting on it, then stop writing it. An
upgrade that fails and goes back to the previous image then never runs
code that needs a field the new worker's documents lack; until the
nightly rebuild, those documents are only found less well.

A field added to documents, or a rule that changes one, e.g. which parts
count as attachments, reaches documents written before it only at the
next successful rebuild: `upgrade.sh` runs one, but keeps the new
release when it fails, and the nightly rebuild then tries again. Until
then a filter on a new field misses the documents that lack it, minutes
for a rebuild that succeeds. When that gap matters, ship the field and
its filterable setting one release before the filter that uses it.
Changing what a field holds is worse: when `internal_date` went from
seconds to milliseconds, old documents fell inside every `before` and
outside every `since` until the rebuild. Until the first release, such a
change, or a removal such as `from_address`'s, goes in one release with
a rebuild or wiped data; after it, add a field under a new name
instead.

To see what the index holds during development, open Meilisearch's
dashboard at `http://localhost:7700`, give it `INBOK_MEILISEARCH_KEY`,
and pick a member's index.

## Logs and request IDs

`serve` writes text logs to stderr, one line per request with its method,
path, status, and duration. The query string is never logged because it may
carry secrets. Server errors (5xx) log at `ERROR` level, except a 503, which
is temporary and logs at `WARN`. A handler panic
returns a 500 Problem Details response and logs `msg=panic` with its stack
trace; the panic value never reaches the client.

Each response has an `X-Request-ID` header, and that request's log lines
carry the same value as `request_id`, including warnings services log while
serving it, e.g. a failed email send. Quote it when reporting a failed
request. An incoming `X-Request-ID` that parses as a UUID, e.g. nginx's
`$request_id`, is reused unchanged; any other value is replaced.

`cmd/inbok` builds one root logger with `logging.New` and passes it to every
service and to `httpapi`. A line gets `request_id` when it is logged with
the request's context, e.g. `logger.WarnContext(ctx, ...)`.

## Checks

From `backend`, check the code:

```sh
go test ./...
go vet ./...
go tool -modfile=tools.mod testifylint ./...
```

[testifylint](https://github.com/Antonboom/testifylint) checks tests
against testify's usage rules, e.g. expected value first; `--fix`
corrects most of them.

Database tests start their own throwaway PostgreSQL in Docker
([Testcontainers](https://golang.testcontainers.org)), Gmail tests their
own [Google emulator](#google-emulator), and the `s3` package's tests
their own SeaweedFS, so Docker must be running; without it they fail
rather than skip. They never touch the Compose services.

## Writing tests

Tests check results with testify's `require` and `assert`
([docs](https://pkg.go.dev/github.com/stretchr/testify)); nothing else
from testify is used. Both report a failure; they differ in what
happens next:

- `require` stops the test. Use it for errors and for any value a later
  line depends on, e.g. a decoded body read by the next check.
- `assert` lets the test go on. Use it for checks that stand alone, so
  one run reports every mismatch.

Put the expected value first, with the same type as the actual one:
`assert.Equal(t, int64(42), total)`; an untyped `42` is an `int` and
never equals an `int64`. `require` must not run in a goroutine the test
started; it can only stop the test's own goroutine.

A JSON number decoded into `any` is a `float64`. Check it with
`assert.InDelta(t, 404, body["status"], 0)`, which is exact. Not
`assert.EqualValues`: it converts `500.5` to the int `500` and passes.

A test reads in three blocks split by blank lines: setup, action, and
checks. An error check that guards the action sits right under it:

```go
cfg, err := Load()
require.NoError(t, err)

assert.Equal(t, "127.0.0.1:8080", cfg.HTTPAddr)
```

When a block is hard to see from its code, e.g. a fake built over
several lines or an action that takes several steps, start each block
with a sentence saying what it does:

```go
// Given storage that loses its connection after the update runs.
storage := &fakeStorage{...}

// When an entry is added.
_, err := service.Add(ctx, entry)

// Then the same error reaches the caller.
require.ErrorIs(t, err, lost)
```

## Migrations

The database schema changes through numbered SQL files, called
migrations, in `backend/internal/repository/postgres/migrations`. Each
file has an `Up` part that applies the change and a `Down` part that
undoes it. `inbok migrate` applies the files the database has not seen
yet and records them; `serve` never does. `migrate` then creates or
upgrades the workflow tables [DBOS](https://docs.dbos.dev) keeps
background work in, in the `dbos` schema. Their migrations ship inside the
DBOS Go SDK, not as files here, and have no Down part. One PostgreSQL lock
covers both steps, so a second `migrate` on the same database waits for
the first.

Upgrading the SDK can bring new workflow table migrations. Review them
like inbok's own: the release before must keep working on the migrated
tables. `dbos.MigrationStatements("dbos", n)` in Go lists the SQL from
version `n` on; `SELECT version FROM dbos.dbos_migrations` shows a
database's current version.

The role in `INBOK_DATABASE_URL` needs the right to create a schema in its
database, and `migrate` also connects to the server's `postgres` database
to check that inbok's exists. The owner a Docker PostgreSQL creates from
`POSTGRES_USER`, as in `compose.yaml`, has both.

Go does not read `.env`, so load it into your shell first. From
`backend`:

```sh
set -a; . ../.env; set +a
go run ./cmd/inbok migrate
```

| Variable | Default | Meaning |
|---|---|---|
| `INBOK_DATABASE_URL` | none, required | PostgreSQL connection URL, e.g. `postgres://inbok:inbok@127.0.0.1:5432/inbok?sslmode=disable`. |

For development, the [Goose](https://pressly.github.io/goose/) command is
pinned in `tools.mod` and reads the `GOOSE_*` settings in `.env`. From
`backend`, after loading `.env`:

```sh
go tool -modfile=tools.mod goose status               # applied and pending files
go tool -modfile=tools.mod goose reset                # run every Down part
go tool -modfile=tools.mod goose create -s add_x sql  # next numbered file
```

Goose knows only inbok's files: `goose reset` and `make db-reset` leave
the `dbos` schema in place. To empty that too, run `DROP SCHEMA dbos CASCADE;` in `psql`; the
next `migrate` creates it again.

Until the first release, inbok is deployed nowhere, so a schema change
edits the migration that made the table rather than adding one that
alters it; inbok keeps no compatibility with unreleased schemas (ADR001,
"Conversations"). A database that already applied it, e.g. your `.env`
stack, then needs `make db-reset`, losing its data, and its accounts
connected again. After the first release, never edit a migration a
deployed database has applied; add a new one.

## Queries

Queries are SQL files in `backend/internal/repository/postgres/queries`.
[SQLC](https://sqlc.dev) reads them together with the migrations and
generates typed Go methods, e.g. `LockProbeCounter`, into
`backend/internal/repository/postgres/sqlc`; there, `models.go` lists
every table's columns. Every feature's queries share that one package, so
a query's name repeats its table's name, e.g. `InsertProbeEvent` for
`probe_events`. A query names every column it reads or returns, never
`*`, even when it takes them all: a later migration that adds a column
then changes no query by surprise, and a reader sees what each query
reads. SQLC still maps a list of every column to the table's model, e.g.
`sqlc.OutgoingMessage`. Settings are in `backend/sqlc.yaml`. After
changing a query or a migration, regenerate from `backend` and commit the
result; never edit it by hand:

```sh
go tool -modfile=tools.mod sqlc generate
```

SQLC parses SQL with PostgreSQL's own C code, so it needs a C compiler,
e.g. gcc. Its first run builds it, which takes about a minute.

## Package layout

The Go code in `backend/internal` has one package per feature for its
rules, and one package per technology that connects those rules to the
outside world. An arrow means "imports":

```text
httpapi --> probe <-- repository/postgres
httpapi --> systememail <-- repository/postgres
                        <-- smtp
httpapi --> emailverification <-- repository/postgres
            emailverification --> systememail
httpapi --> registration <-- repository/postgres
            registration --> systememail, account
httpapi --> passwordreset <-- repository/postgres
            passwordreset --> systememail, account
httpapi --> connectedaccount <-- repository/postgres
                             <-- google
            outgoingmessage <-- repository/postgres
                            <-- google
httpapi --> mailsync <-- repository/postgres
            mailsync --> connectedaccount
                     <-- google
httpapi --> conversation <-- repository/postgres
                         <-- google
            conversation --> mailsync, connectedaccount
httpapi --> search <-- meilisearch
                   <-- repository/postgres
            search --> mailsync, messagecontent
httpapi --> messagecontent <-- s3
                           <-- google
                           <-- repository/postgres
            messagecontent --> connectedaccount, mailsync
```

- `probe`, like every feature package, holds the feature's types, its
  rules, its `Service`, and a `Storage` interface listing what it needs
  saved. It imports no Gin, Huma, pgx, or SQLC, and never imports
  `httpapi` or `repository/postgres`, so its tests need no server and no
  database.
- `httpapi` registers every feature's operations and holds the shared
  middleware and error handling. For each feature it declares an
  interface for what its operations call, e.g. `probeService`, so its
  tests answer with fixed results instead of running the feature's rules.
- `repository/postgres` implements every feature's `Storage` on one `DB`.
  A write that must save several features' data together is one `DB`
  method running one transaction, as `UpdateCounter` does. `DB` method
  names must be unique across features; when two clash, qualify the newer
  one.
- `smtp`, like `repository/postgres`, is a technology package: it
  implements the `Sender` interface that `systememail` declares, using the
  emersion SMTP libraries. A technology package is named for its technology
  and imports features, never the other way around. `meilisearch`
  implements `search`'s `Index` the same way.
- `s3` is the technology package for the
  [Message content store](#message-content-store): it implements the
  `Store` interface that `messagecontent` declares, using AWS's Go SDK,
  whose types never leave the package.
- A leaf package holds one rule that several features share, such as
  `emailaddress` (the address rule), `attemptlimit` (the window count),
  `msgid` (which msg-ids inbok writes, and how References is trimmed,
  for sync and replies), `randomsecret` (emailed link secrets and their hashes), `clientprefix`
  (which addresses count as one client), or `logging` (how loggers are
  built and tagged with the request). It
  imports no feature or technology package and is named for its rule.
- A feature can use another feature through an interface it declares:
  `emailverification` sends through a `mailer` interface, which `cmd/inbok`
  fills with `systememail`'s `Mailer`. A feature may import another's plain
  rules directly: `registration` uses `account.ValidatePassword` and
  `account.User`.
- `google`, like `smtp`, is a technology package: it implements the
  Google side `connectedaccount` declares for its connect flow, its token
  access, and its revocations: the OAuth client, the ID token checks, the
  refresh token trade, revocation, and Gmail's `users.getProfile`;
  `outgoingmessage`'s `Provider`: `messages.send` and the check after an
  unanswered send; `mailsync`'s `Provider`, the Gmail reads
  synchronization makes; and `conversation`'s `ThreadChanger`, the
  thread calls a member's actions make. It uses `golang.org/x/oauth2`
  and Google's Gmail client, whose types stay inside it; Gmail calls take
  `connectedaccount`'s `AccessToken`. It also implements
  `messagecontent`'s `Provider`, a message's content in full format and
  one part's bytes, turning a Gmail message's parts into
  `messagecontent.Received`, the provider-neutral input of
  [Message parts](#message-parts).
- `googleemulator` starts the [Google emulator](#google-emulator) for
  tests, each with its own seed. Only tests import it.
- `webapp` embeds the built web app and serves it as a static site:
  files, the shell for browser routes, caching, and the CSP. It knows the
  build's layout, not the web app's code. `httpapi` declares a `webApp`
  interface and sends it the reads outside `/api`, keeping the API's 404
  and 405 for the rest, so its tests use a fake.
- `dbos` is the technology package for [Background work](#background-work):
  it opens DBOS for the worker, registers and schedules its workflows,
  launches it, and shuts it down. Its steps call features through
  interfaces it declares, in `dbos.Services`, which `cmd/inbok` fills, e.g.
  with `emailverification.Cleanup`; like other technology packages it may
  import a feature's plain types, e.g. `account.NoticeOutcome`, while
  features never import DBOS. Its `Client` saves workflows inside a storage
  transaction, from `recover-admin`, `serve`, or a worker's own steps: it
  implements `repository/postgres`'s `Workflows`, and the two meet on
  pgx's `pgx.Tx`, which DBOS's API takes, without importing each other
  outside tests. It is the only package besides `repository/postgres`
  that imports the DBOS SDK, whose package is also named `dbos`.
- `cmd/inbok` builds each feature's `Service` and hands it to `httpapi`
  in `httpapi.Services`, and loads `webapp`'s build for `ServeWebApp`.

ADR021 in the project's decision records explains this layout and the
alternatives it was chosen over.

### Files and sections

A package splits into files by concept, named for its noun, as `smtp`
does with `compose.go`, `signin.go`, and `redaction.go`. Give a part its
own file when it stands alone and is worth testing on its own, such as
`s3`'s `contentkey.go` (building and parsing object keys) beside
`content.go` (the S3 calls). Within a long file, a three-line banner
comment groups related code for readability, as
[HTTP feature files](#http-feature-files) do for each operation:

```go
// ========================
// First administrator
// ========================
```

### HTTP feature files

Keep shared paths, service interfaces, handler dependencies, and the
`register<Feature>` entry point at the top. That entry point calls a
`register<Operation>` function for each operation.

Group each operation under a banner (see
[Files and sections](#files-and-sections)), and keep its constants,
request and response types, input resolvers, registration, named handler,
response helper, and error mapping together under that banner. Helpers
used by only one operation stay in its section. Handler types and
methods are private; operations without dependencies can use plain functions.

## Adding a feature

To add a feature, e.g. `account`:

1. `internal/account`: types, rules, `Service`, and `Storage`, tested with
   an in-memory `Storage`.
2. A migration and `queries/account.sql`; regenerate SQLC.
3. `repository/postgres/account.go`: the `Storage` methods, tested against
   PostgreSQL.
4. `httpapi/account.go`: follow [HTTP feature files](#http-feature-files).
   Declare an `accountService` interface and a `registerAccount` entry point.
   Test the operations with fake services. Add an `Account` field to
   `Services` and call `registerAccount` in `New`, before
   `registerProblemTypes`, which lists the problem types registered so far.
5. `cmd/inbok/main.go`: set `Account` in `serve`'s `Services`.
6. Regenerate `api/generated/openapi.json`.

Each step can be its own commit.

## Adding a background job

Background work runs in `inbok worker` on DBOS; read
[Background work](#background-work) first. A job is a **workflow** made of
**steps**: DBOS records each finished step's result and, after a stop,
resumes at the first step without one. The rules every job follows:

- A step may run twice: when the worker stops during it, it runs again.
  Make each step safe to repeat, e.g. a delete, or an insert that ignores
  a row it already added. Where it cannot be, e.g. an email, decide
  whether a repeat is acceptable, as the recovery notice accepts a second
  copy, and record why in ADR004.
- Steps call plain feature methods. Feature packages never import DBOS;
  `internal/dbos` declares a small interface for what it calls and a field
  for it in `dbos.Services`, which `cmd/inbok/worker.go` fills, as `serve`
  fills `httpapi.Services`. `dbos.NewWorker` registers every workflow and
  schedule, as `httpapi.New` registers every operation.
- Bulk work runs in bounded batches, one step each, so a run makes durable
  progress and no statement runs long.
- Names DBOS records outlive the code: give the workflow a fixed name with
  `dbos.WithWorkflowName`, keep step names, and add steps at the end. A run
  an older release started resumes on the new code, and DBOS fails a step
  whose name differs from the one it recorded at that position.
- Inputs and outputs are saved as JSON in the database and kept with the
  run. Give a workflow IDs, and facts that are not personal data and
  will not change, e.g. when the recovery happened; let its steps read
  the rest. A step's output holds what later steps need to resume, e.g.
  the recovery notice's list of administrator IDs, and never a secret or
  personal data a later step can read itself.

To add work to the [scheduled cleanup](#scheduled-cleanup), e.g. expired
rows of a new table, as password reset's links and attempts were added:

1. The feature: a `Cleanup` type whose `DeleteExpired(ctx)` deletes one
   batch and returns how many and whether more may remain, tested with an
   in-memory storage, as `internal/emailverification/cleanup.go` does.
2. `repository/postgres`: a query deleting up to a batch of expired rows
   with `FOR UPDATE SKIP LOCKED`, and its `DB` method, tested against
   PostgreSQL, as `DeleteExpiredEmailVerificationLinks` is.
3. `internal/dbos`: a field in `Services`, and its entry at the end of
   `Worker.cleanups` in `cleanup.go`, with a name that stays fixed.
4. `cmd/inbok/worker.go`: set the field to the feature's `Cleanup`.

For a new scheduled job, follow `internal/dbos/cleanup.go`: register the
workflow under a fixed name and save its schedule with
`dbos.ApplySchedules`, both called from `NewWorker`.

For a job that runs once per request, follow the recovery notice
(`internal/account/recoverynotice.go`, `internal/dbos/recoverynotice.go`):

1. The feature: a type whose methods are the steps, each returning a
   value DBOS can save as JSON, e.g. the IDs a later step works through.
   Return an error only for a failure worth retrying; end the job with an
   outcome value for one that is not.
2. `internal/dbos`: an input struct holding IDs and no personal data,
   the workflow registered under a fixed name with each step's retry
   policy, and a field in `Services` for the feature's type. A job that sends inbok's
   email runs on `inbok.email`, one that calls Google in the background on
   `inbok.google`, and one a member waits for on Gmail, a send, on
   `inbok.google-send` (`queue.go`), so it never waits behind long
   background runs; another kind of job adds its own queue there.
3. `cmd/inbok/worker.go`: set that field.
4. Save the workflow with the change that needs it, in one transaction.
   The feature's `Storage` method documents that it saves the job; its
   `repository/postgres` implementation calls a method of the
   `Workflows` interface with the transaction, which `dbos.Client`
   implements with `dbos.Enqueue` and `dbos.WithEnqueueTransaction`, as
   `UpdateRecovery` and `EnqueueRecoveryNotice` do. Wrap the transaction
   in `callerTx`, so cancelling the caller stops the enqueue; DBOS alone
   would not. The feature never sees the transaction or DBOS. A test
   makes the change fail at commit and checks that no workflow was saved.

   The method belongs to the feature's own interface, `<Feature>Workflows`,
   which `Workflows` embeds, as it embeds `RecoveryWorkflows`; a feature's
   first job adds that interface. Its test fake embeds a nil `Workflows`
   and implements only the feature's interface, as `tableWorkflows` does,
   so another feature's job changes no fake of yours.
5. The command that makes the change gives storage the client with
   `DB.WithWorkflows`, as `recover-admin` does; `serve` already has one,
   for jobs requests save, and so has the worker, for jobs its steps save.

### Calling Gmail

A job that calls Gmail, e.g. synchronization or sending, takes an access
token for the account from `connectedaccount.Access.Token` in each step
that calls Gmail, and passes it to the `google` client's Gmail methods.
A token lasts as long as Google's `expires_in` said, usually an hour.
`Token` does what Google's refresh tokens need:

- It keeps each account's access token in the worker's memory, never
  stored, and hands it out again while its refresh token is still the
  saved one and more than 5 minutes of it remain, a margin for the step
  that takes it. Every call still reads the account first, so every stop
  error comes as before, and drops the token of an account found stopped;
  keeping a new token drops the expired ones. When any Gmail call
  answers 401, the `google` client's transport calls the token's
  `Refused`, which drops it from `Access` unless a newer one replaced it,
  so the job's next try, a send's or a sync's, trades the refresh token
  again. No feature handles the 401 itself.

- It saves a replacement refresh token Google sends before handing out
  the access token, and never revokes the one replaced.
- When Google refuses the refresh token (`invalid_grant`), it marks the
  account as needing a reconnect and returns
  `connectedaccount.ErrNeedsReconnect`; it returns that again, without
  asking Google, until the member reconnects. A job stops work for the
  account on it, as on `ErrNotFound` for a disconnected account and
  `ErrDeactivated` for a deactivated member's.
- When Google's token endpoint fails instead, e.g. in an outage, its
  error wraps `connectedaccount.ErrRefreshFailed`, so a job can tell
  Google's failure from its own storage's, which `Token` returns as is.
- `Access.Check` answers the same stop errors, `ErrTokenUnreadable`
  included, without asking Google, for a step that writes only inbok's
  data but must still stop for a stopped account.
- It writes only against the token it read, so a reconnect or another
  job's replacement in between wins; it then reads again once, and
  returns `ErrCredentialChanged` if the token changed again. Saving a
  replacement clears a refusal another job recorded against the same
  token, since Google accepted that token to send the replacement.
- It reads the account under the member's share lock, as
  ADR029 asks before provider work, and reads it again just before
  returning, so an account disconnected, deactivated, or refused while
  Google answered gets no token. Its checks hold at that last read; a job
  that must not act for an account gone since, e.g. one sending mail,
  checks again where it acts.

Build `Access` in `cmd/inbok/worker.go` only when a Google client is
configured, as `connectedaccount.Removals` is, so the job's field in
`dbos.Services` stays nil otherwise: a nil `*Access` stored in it would
pass the worker's nil check.

The `mailsync.Provider` methods back off Gmail's rate limits themselves,
as [Synchronized mail](#synchronized-mail) describes, and return
`mailsync.ErrProviderBusy` once the retries run out.

Keep the token out of a step's output: DBOS saves outputs in the
database. `AccessToken` saves as `{}` in JSON and prints as a
placeholder, so a slip shows nothing, but a step should return what
later steps need, never the token.
