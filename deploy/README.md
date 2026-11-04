# Self-hosting inbok

This folder runs inbok on one server with Docker Compose: PostgreSQL, a
one-off migration, the inbok server, its background worker, and Caddy for
HTTPS. No inbok image is
pulled from a registry: Compose builds it on the server from this
repository, using pinned base images, and runs it beside pinned PostgreSQL
and Caddy images.

Run every command here from this folder, `deploy/`.

## What you need

- A Linux server with Docker Engine 28 or newer and its Compose plugin
  (`docker version`, `docker compose version`), plus git. Older engines
  let other machines on the same network segment reach ports published to
  localhost, and before 27 Docker kept IPv6 visitors' addresses only if
  `ip6tables` was turned on by hand.
- A domain, e.g. `inbok.example.com`, whose DNS points at the server.
- Ports 80 and 443 open on TCP, and 443 on UDP for HTTP/3. Caddy gets
  its certificate through them.

## First run

1. Get the code and the settings file:

   ```sh
   git clone <repository URL> inbok
   cd inbok/deploy
   cp .env.example .env && chmod 600 .env
   ```

2. Fill in every empty value in `.env`. Each one says how to generate it.
   Keep a copy of `INBOK_ENCRYPTION_KEYS` somewhere other than the server,
   e.g. a password manager: see [Backups](#backups). `INBOK_PUBLIC_URL`
   is already filled in from `INBOK_DOMAIN`.

3. Start it. The first build takes a few minutes:

   ```sh
   docker compose up -d --build
   ```

   `docker compose ps -a` shows `postgres`, `inbok`, and `worker` as
   `healthy`, `caddy` running, and `migrate` exited with code 0.

4. Open `https://<your domain>/setup`, enter `INBOK_SETUP_TOKEN`, and
   create the first administrator. Then sign in and set up two-factor
   authentication on the Security page; an administrator needs it.

5. Empty `INBOK_SETUP_TOKEN` in `.env` and apply it:

   ```sh
   docker compose up -d
   ```

Logs come from `docker compose logs inbok` (or `worker`, `caddy`,
`postgres`, `migrate`). Every line inbok logs for a request carries its
`request_id`, which the browser also receives in the `X-Request-ID` header.

## The worker

`worker` runs inbok's background work, which it keeps in the database, so
work survives a restart. It reaches PostgreSQL and, to send inbok's
email, mail servers on the internet; it needs `INBOK_ENCRYPTION_KEYS` and
`INBOK_PUBLIC_URL` like the server. It answers its health check on its own
loopback address, so nothing else can reach it. Stopping, restarting, or replacing it is
safe: work it left unfinished resumes when it starts again, from the step
that had not finished, so a step can run twice. That relies on its
`INBOK_WORKER_ID`, which defaults to `worker`; keep it. Do not scale it,
e.g. with `docker compose up --scale worker=2`: both copies would share
one ID and run some work twice. One worker runs many jobs at once.
[Background work](../backend/README.md#background-work) explains the rest.

## Upgrades

Take a [backup](#backups) first: migrations have no undo. Then:

```sh
git pull
./upgrade.sh
```

`upgrade.sh` builds a candidate image, runs the new migrations with it
while the old server keeps serving, and only then switches the image and
restarts the server and the worker. The site is unavailable for about two
seconds while the server restarts; the new worker resumes the work the
old one left unfinished. The script exits with an error if anything fails:

- If the build or a migration fails, the old server keeps running on the
  old image. Fix the cause, or check out the previous commit, and run
  `./upgrade.sh` again.
- If the new server or worker does not become healthy, the script starts
  the previous image again, on the migrated database. `docker compose logs
  inbok` or `worker` shows why the new one failed.

Avoid two commands that look like shortcuts:

- `docker compose up -d --build` removes the old server before migrating,
  so a failing migration takes the site down until it is fixed.
- `docker compose restart` restarts every service at once, so migrate can
  start before PostgreSQL and fail. Use `docker compose up -d` to restart.

After a migration, the old server and worker can run on the new schema:
during an upgrade, and after the rollback above. Every migration must
therefore keep the previous release working: add tables and columns, and
remove old ones only in a later release. Background work a release left
unfinished resumes in the next one, so it must keep working across
releases too.

## Backups

A backup has two parts, kept apart from each other.

The database: dump it with PostgreSQL's own tool, into a folder outside
this checkout that only you can read:

```sh
mkdir -p ~/inbok-backups
(umask 077; docker compose exec -T postgres pg_dump -U inbok --format=custom inbok > ~/inbok-backups/inbok-$(date +%F).dump)
```

The encryption keys: `INBOK_ENCRYPTION_KEYS` from `.env`. They decrypt
the TOTP secrets and the system email password stored in the database,
so a stolen dump alone reveals neither, and a restore needs every key its
rows use. Store them elsewhere, e.g. in a password manager, and update
that copy when you rotate keys.

To restore, stop the services that use the database, load the dump, and
start them again. `--single-transaction` makes the restore all or
nothing: on any error it stops and leaves the database as it was. The dump
holds background work as it stood when it was taken: work unfinished then
runs again after the restore, including steps that had finished since.

```sh
docker compose stop inbok worker caddy
docker compose exec -T postgres pg_restore -U inbok -d inbok --clean --if-exists --no-owner --single-transaction < ~/inbok-backups/inbok-2026-11-01.dump
docker compose up -d
```

On a new server, run `docker compose up -d --wait postgres` first, which
creates the empty database, then the restore and `docker compose up -d`.
Use the same `INBOK_ENCRYPTION_KEYS` as when the dump was made. The
`caddy-data` volume needs no backup: Caddy requests new certificates if
it is empty.

`docker compose down -v` deletes the volumes, including the only copy of
the database. Plain `docker compose down` keeps them.

## Administrator recovery

For an administrator who lost their password, authenticator, and backup
codes:

```sh
docker compose exec inbok inbok recover-admin admin@example.com
```

It asks for the new password twice without echoing it, shows what it
will change, and asks to continue. To supply the password from a password
manager instead, pipe one line in and add `-T`, which skips the
confirmation:
`docker compose exec -T inbok inbok recover-admin admin@example.com`. The
administrator then signs in and sets up two-factor authentication again.
The command also saves emails telling every active administrator of the
recovery, which the `worker` service sends; its last line says whether
they will go out.
[Administrator recovery](../backend/README.md#administrator-recovery)
explains what the command changes.

## Using your own proxy instead of Caddy

If the server already runs nginx or another proxy, turn Caddy off and
publish inbok on loopback for that proxy. Create `compose.override.yaml`
next to `compose.yaml`; Compose and `upgrade.sh` read it automatically:

```yaml
services:
  caddy:
    # Starts only with --profile caddy, so up and upgrade.sh skip it.
    profiles: ["caddy"]
  inbok:
    ports:
      - 127.0.0.1:8080:8080
    environment:
      # A proxy on this host reaches the published port through the edge
      # network's gateway, so that address sends the client's address.
      INBOK_TRUSTED_PROXIES: 10.83.70.8/32
```

If Caddy already ran, remove it with `docker compose rm -sf caddy`, then
run `docker compose up -d`. Only programs on the server can reach
`127.0.0.1:8080`, and every one of them arrives from that gateway, so
any of them could claim a client address. This setup assumes no
untrusted users on the server, as a host proxy does anyway.

If your proxy serves inbok on a name other than `INBOK_DOMAIN`, or on a
port other than 443, set `INBOK_PUBLIC_URL` in `.env` to that address.
Links in inbok's emails start with it.

Your proxy must serve HTTPS, since inbok's cookies are HTTPS-only, and:

- keep the browser's `Host` header, which inbok compares with `Origin`;
- set `X-Forwarded-For` to the client's address, replacing any value the
  client sent;
- pass `Origin` and `Sec-Fetch-Site` through unchanged;
- send `Strict-Transport-Security` if you want HSTS, as Caddy does here.

[Reverse proxies](../backend/README.md#reverse-proxies) has an nginx
server block that does this; point its `proxy_pass` at
`http://127.0.0.1:8080`.

## Good to know

### Letting others create accounts

Registration starts closed, so the first administrator is the only
account. To let others join, the administrator, once two-factor
authentication is set up, opens Settings, saves the system email settings,
sends a test message that passes, and opens registration there. Visitors
then find "Create an account" on the sign-in page and get their sign-up
link by email.

Anyone who can reach your domain can join, and each member adds storage
and sync work to the server. If only some people should join, close
registration again once they have, or keep inbok behind a VPN. inbok
sends at most 50 registration emails an hour, links and notices for all
visitors together, so strangers cannot make it send unlimited mail through
your mail account.

### Firewalls

Only Caddy publishes ports. Docker's port publishing bypasses host
firewalls such as ufw, so a port you publish yourself is reachable even
if the firewall says otherwise. Bind it to `127.0.0.1`, as above, unless
it must be public.

### IPv6

The edge network has IPv6, so Caddy sees each visitor's real address.
That needs Docker's `ip6tables` setting, which is on by default.

### Address conflicts

The networks use fixed subnets, `10.83.70.0/28` and
`fd24:44bd:915f::/64`, so inbok trusts exactly Caddy's addresses. If
Docker reports that a pool overlaps, change them in `compose.yaml` as its
comment says. For the same reason only one such stack fits on a host.

### Changing the Caddyfile

Edit `caddy/Caddyfile`, then reload Caddy without a restart:

```sh
docker compose exec -w /etc/caddy caddy caddy reload
```

### Rotating keys and the database password

`.env.example` gives the steps; [Encryption keys](../backend/README.md#encryption-keys)
explains rotation and what a lost key breaks.

### A trial on localhost

Use a machine with no other inbok stack: the fixed subnets allow one per
host, and every stack builds the same `inbok:local` image. Copy
`.env.example` to `.env.trial`, which git ignores, fill it in with
`INBOK_DOMAIN=localhost`, and set `INBOK_CADDY_HTTP_PORT` and
`INBOK_CADDY_HTTPS_PORT` if 80 and 443 are taken. With another HTTPS port,
set `INBOK_PUBLIC_URL` to match, e.g. `https://localhost:8443`, or links
in inbok's emails point at port 443. Add
`COMPOSE_PROJECT_NAME=inbok-trial` to it, so every command with this file
works on the trial's own containers and volumes:

```sh
docker compose --env-file .env.trial up -d --build
docker compose --env-file .env.trial down -v
```

Caddy then uses a certificate from its own local CA, which browsers warn
about.

### Build caches

Builds keep downloaded Go modules, compiled packages, and web packages in
Docker's build cache, so upgrades rebuild in seconds. `docker build
--no-cache` builds without them but keeps them. To delete inbok's
caches:

```sh
docker buildx prune --filter type=exec.cachemount --filter 'description~=inbok-(gomod|gobuild|pnpm)'
```
