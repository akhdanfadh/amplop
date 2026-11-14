#!/bin/sh
# Upgrades a running inbok to the checked-out code: builds a candidate
# image, migrates the database with it while the old server keeps serving,
# then starts the services that do not run the inbok image, e.g. the search
# index, deletes the search index's volumes of other Meilisearch versions,
# makes the candidate the inbok image, replaces the server and the worker,
# and rebuilds the search index with the new release. If the build,
# the migration, or one of those services fails, the old server keeps
# running. If the new server or worker does not become healthy, the
# previous image starts again. If the rebuild fails, the new release keeps
# running. Each failure exits with an error. Arguments go to every docker
# compose command, e.g. ./upgrade.sh --env-file .env.trial, except
# --dry-run, which it refuses.
#
# NOTE: A plain docker compose up -d --build removes the old server before
# migrate runs, so a failing migration would take the site down. Between
# migrate and the new server, and after a rollback, the old server runs on
# the new schema, so each migration must keep the previous release working.
set -eu

# Compose reads compose.yaml, compose.override.yaml, and .env from here,
# and relative paths in the arguments from where the script was run.
dir=$(dirname "$0")

# Steps 3b and 4 change Docker itself, outside Compose, so Compose's
# --dry-run would still delete volumes and switch the image.
for arg in "$@"; do
	if [ "$arg" = --dry-run ]; then
		echo "upgrade.sh: --dry-run is not supported: steps outside Compose would still delete volumes and switch the image" >&2
		exit 2
	fi
done

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

# 3b. Delete the volumes of other Meilisearch versions, once the new one
# is healthy and nothing mounts them. Each holds a copy of members' mail
# from an earlier state of the database, which a return to that version
# would search until its rebuild ends: after a restore gives its user IDs
# to new members, another member's mail. Going back then starts on an empty
# volume, which step 5 fills, as going forward does. Compose labels each
# volume with its project and its key in compose.yaml.
# Each command's output is assigned on its own, so its failure stops the
# script, rather than skipping the cleanup unseen.
config=$(docker compose --project-directory "$dir" "$@" config)
project=$(printf '%s\n' "$config" | sed -n 's/^name: //p')
case $project in
"" | *"
"*)
	echo "upgrade.sh: docker compose config names no single project, so old search volumes were not deleted" >&2
	exit 1
	;;
esac
current=$(docker compose --project-directory "$dir" "$@" config --volumes)
volumes=$(docker volume ls -q --filter "label=com.docker.compose.project=$project")
# $volumes is unquoted on purpose: volume names have no spaces.
for volume in $volumes; do
	key=$(docker volume inspect --format '{{index .Labels "com.docker.compose.volume"}}' "$volume")
	# Only a version's volume, as compose.yaml names them.
	case $key in
	meilisearch-[0-9]*) ;;
	*) continue ;;
	esac
	if printf '%s\n' "$current" | grep -q -x -F -e "$key"; then
		continue
	fi
	docker volume rm "$volume"
done

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

# 5. Rebuild the search index with the new release, which may write
# documents differently or bring a new, empty Meilisearch. After the switch,
# not before: the rebuild must run the new code. Going back to the
# previous image would not undo a failed rebuild, so the new release stays.
if ! docker compose --project-directory "$dir" "$@" exec -T worker inbok search rebuild; then
	echo "upgrade.sh: the new release is running, but the search index was not rebuilt; run docker compose exec worker inbok search rebuild, with the same arguments" >&2
	exit 1
fi
