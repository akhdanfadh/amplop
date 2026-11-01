#!/bin/sh
# Upgrades a running inbok to the checked-out code: builds a candidate
# image, migrates the database with it while the old server keeps serving,
# then makes it the inbok image and replaces the server. If the build or
# the migration fails, the old server keeps running. If the new server does
# not become healthy, the previous image starts again. Either way the script
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

INBOK_IMAGE=inbok:candidate docker compose --project-directory "$dir" "$@" build
INBOK_IMAGE=inbok:candidate docker compose --project-directory "$dir" "$@" run --rm migrate

previous=$(docker image inspect --format '{{.Id}}' inbok:local 2>/dev/null || true)
docker tag inbok:candidate inbok:local
if ! docker compose --project-directory "$dir" "$@" up -d --wait; then
	if [ -n "$previous" ]; then
		echo "upgrade.sh: the new server is not healthy; starting the previous image again" >&2
		docker tag "$previous" inbok:local
		docker compose --project-directory "$dir" "$@" up -d --wait
	fi
	exit 1
fi
