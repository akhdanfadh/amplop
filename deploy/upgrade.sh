#!/bin/sh
# Upgrades a running amplop to the checked-out code: builds a candidate
# image, migrates the database with it while the old server keeps serving,
# then starts the services that do not run the amplop image, e.g. the search
# index, deletes the search index's volumes of other Meilisearch versions,
# makes the candidate the amplop image, replaces the worker and then the
# server, and rebuilds the search index with the new release. If the
# build, the migration, or one of those services fails, the old server
# keeps running. If the new worker or server does not become healthy, the
# previous image starts again, the server before the worker; if the
# previous server fails too, the new worker stays. If the rebuild fails,
# the new release keeps running. Each failure exits with an error.
# Arguments go to every docker compose command, e.g. ./upgrade.sh
# --env-file .env.trial, except --dry-run, which it refuses.
#
# A plain docker compose up -d --build removes the old server before
# migrate runs, so a failing migration would take the site down. Between
# migrate and the new server, and after a rollback, the old server runs on
# the new schema, and beside the new worker's search index settings, so
# each migration and each settings change must keep the previous release
# working. upgrade.bats checks the order and the rollbacks: change it with
# the steps.
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
AMPLOP_IMAGE=amplop:candidate docker compose --project-directory "$dir" "$@" build

# 2. Migrate with it while the old server keeps serving.
AMPLOP_IMAGE=amplop:candidate docker compose --project-directory "$dir" "$@" run --rm migrate

# 3. Start every service the amplop image does not run, e.g. Meilisearch.
# Going back to the previous image cannot repair one, so its failure stops
# here, before the switch. Caddy waits for amplop, so it starts in step 4.
# A service an override adds, or a profile turns off, is covered or left
# out by config --services.
others=$(docker compose --project-directory "$dir" "$@" config --services | grep -v -x -e migrate -e amplop -e worker -e caddy || true)
# $others is unquoted on purpose: service names have no spaces, so word
# splitting passes one argument per service.
if [ -n "$others" ] && ! docker compose --project-directory "$dir" "$@" up -d --wait $others; then
	echo "upgrade.sh: a service above is not healthy, so the amplop image is unchanged; docker compose logs <service> shows why" >&2
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

# 4. Switch the image, and replace the worker before the server. The new
# worker applies the search index's settings before it reports healthy,
# and once it runs, no old worker is left to write documents with the old
# settings, which every write applies. So the new server, which may filter
# on a field only the new settings allow, never meets an index without
# them. Meanwhile the old server keeps serving, and the jobs it queues wait
# for the new worker.
previous=$(docker image inspect --format '{{.Id}}' amplop:local 2>/dev/null || true)
docker tag amplop:candidate amplop:local
if ! docker compose --project-directory "$dir" "$@" up -d --wait --no-deps worker; then
	if [ -n "$previous" ]; then
		echo "upgrade.sh: the new worker is not healthy; starting the previous image's worker again" >&2
		docker tag "$previous" amplop:local
		docker compose --project-directory "$dir" "$@" up -d --wait --no-deps worker
	fi
	exit 1
fi

# 4b. Replace the server, and anything else the new image changes.
if ! docker compose --project-directory "$dir" "$@" up -d --wait; then
	if [ -n "$previous" ]; then
		echo "upgrade.sh: the new server is not healthy; starting the previous image again, the server first" >&2
		docker tag "$previous" amplop:local
		# The server before the worker, the forward order reversed: the
		# previous worker applies its own settings, which the new server,
		# were it still running, could not search with. When the previous
		# server fails too, the new worker and its settings stay.
		if docker compose --project-directory "$dir" "$@" up -d --wait --no-deps amplop; then
			docker compose --project-directory "$dir" "$@" up -d --wait
		else
			echo "upgrade.sh: the previous server is not healthy either; the new worker keeps running" >&2
		fi
	fi
	exit 1
fi

# 5. Rebuild the search index with the new release, which may write
# documents differently or bring a new, empty Meilisearch. After the switch,
# not before: the rebuild must run the new code. Going back to the
# previous image would not undo a failed rebuild, so the new release stays.
if ! docker compose --project-directory "$dir" "$@" exec -T worker amplop search rebuild; then
	echo "upgrade.sh: the new release is running, but the search index was not rebuilt; run docker compose exec worker amplop search rebuild, with the same arguments" >&2
	exit 1
fi
