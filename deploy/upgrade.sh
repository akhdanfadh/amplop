#!/bin/sh
# Upgrades a running inbok to the checked-out code: builds a candidate
# image, migrates the database with it while the old server keeps serving,
# then starts the services that do not run the inbok image, e.g. the search
# index, makes the candidate the inbok image, and replaces the server and
# the worker. If the build, the migration, or one of those services fails,
# the old server keeps running. If the new server or worker does not
# become healthy, the previous image starts again. Either way the script
# exits with an error. Arguments go to every docker compose command, e.g.
# ./upgrade.sh --env-file .env.trial.
#
# NOTE: A plain docker compose up -d --build removes the old server before
# migrate runs, so a failing migration would take the site down. Between
# migrate and the new server, and after a rollback, the old server runs on
# the new schema, so each migration must keep the previous release working.
set -eu

# Compose reads compose.yaml, compose.override.yaml, and .env from here,
# and relative paths in the arguments from where the script was run.
dir=$(dirname "$0")

# 1. Build the candidate.
INBOK_IMAGE=inbok:candidate docker compose --project-directory "$dir" "$@" build

# 2. Migrate with it while the old server keeps serving.
INBOK_IMAGE=inbok:candidate docker compose --project-directory "$dir" "$@" run --rm migrate

# 3. Start every service the inbok image does not run, e.g. Meilisearch.
# Going back to the previous image cannot repair one, so its failure stops
# here, before the switch. Caddy waits for inbok, so it starts in step 4.
# A service an override adds, or a profile turns off, is covered or left
# out by config --services.
others=$(docker compose --project-directory "$dir" "$@" config --services | grep -v -x -e migrate -e inbok -e worker -e caddy || true)
# $others is unquoted on purpose: service names have no spaces, so word
# splitting passes one argument per service.
if [ -n "$others" ] && ! docker compose --project-directory "$dir" "$@" up -d --wait $others; then
	echo "upgrade.sh: a service above is not healthy, so the inbok image is unchanged; docker compose logs <service> shows why" >&2
	exit 1
fi

# 4. Switch the image and replace the server and the worker.
previous=$(docker image inspect --format '{{.Id}}' inbok:local 2>/dev/null || true)
docker tag inbok:candidate inbok:local
if ! docker compose --project-directory "$dir" "$@" up -d --wait; then
	if [ -n "$previous" ]; then
		echo "upgrade.sh: the new server or worker is not healthy; starting the previous image again" >&2
		docker tag "$previous" inbok:local
		docker compose --project-directory "$dir" "$@" up -d --wait
	fi
	exit 1
fi
