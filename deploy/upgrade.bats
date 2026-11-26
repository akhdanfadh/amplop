#!/usr/bin/env bats
# Tests upgrade.sh's order and rollbacks without Docker: testdata/fake-docker,
# first on PATH, records each call that changes something, with the image
# amplop:local points to at that moment, answers only the questions the
# script should ask, and fails the calls a test names.
# It checks only the script's decisions; what Docker does with each call
# is a trial stack's to check. Run it with make check-deploy.

setup() {
	SCRIPT="$BATS_TEST_DIRNAME/upgrade.sh"
	export WORK="$BATS_TEST_TMPDIR"
	mkdir "$WORK/bin"
	cp "$BATS_TEST_DIRNAME/testdata/fake-docker" "$WORK/bin/docker"
	echo previous > "$WORK/image"
	: > "$WORK/calls"
	: > "$WORK/all"
	PATH="$WORK/bin:$PATH"
}

# calls_are compares the calls made with stdin.
calls_are() {
	diff -u - "$WORK/calls"
}

# The calls every upgrade makes before it switches the image.
before_switch='AMPLOP_IMAGE=amplop:candidate [previous] docker compose -p probe build
AMPLOP_IMAGE=amplop:candidate [previous] docker compose -p probe run --rm migrate
[previous] docker compose -p probe up -d --wait postgres meilisearch seaweedfs'

@test "upgrade" {
	run sh "$SCRIPT" -p probe
	[ "$status" -eq 0 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker compose -p probe up -d --wait
[amplop:candidate] docker compose -p probe exec -T worker amplop search rebuild
EOF
}

@test "a failed migration keeps the old image" {
	FAIL='run --rm migrate' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<'EOF'
AMPLOP_IMAGE=amplop:candidate [previous] docker compose -p probe build
AMPLOP_IMAGE=amplop:candidate [previous] docker compose -p probe run --rm migrate
EOF
}

@test "a service that fails keeps the old image" {
	FAIL='seaweedfs' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
EOF
}

# A volume an override adds, e.g. for dumps, is not a version's; a version
# whose name starts the current one's, 1.5 of 1.56.1, is another version.
@test "only other Meilisearch versions' volumes are deleted" {
	VOLUMES='probe_db=db
probe_meilisearch-1.55.0=meilisearch-1.55.0
probe_meilisearch-1.5=meilisearch-1.5
probe_meilisearch-1.56.1=meilisearch-1.56.1
probe_meilisearch-dumps=meilisearch-dumps' run sh "$SCRIPT" -p probe
	[ "$status" -eq 0 ]
	calls_are <<EOF
$before_switch
[previous] docker volume rm probe_meilisearch-1.55.0
[previous] docker volume rm probe_meilisearch-1.5
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker compose -p probe up -d --wait
[amplop:candidate] docker compose -p probe exec -T worker amplop search rebuild
EOF
}

@test "a failed worker brings back only the previous worker" {
	FAIL='--no-deps worker' FAIL_IMAGE=amplop:candidate run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker tag previous amplop:local
[previous] docker compose -p probe up -d --wait --no-deps worker
EOF
}

@test "a failed server brings back the previous server, then worker" {
	FAIL='--wait$' FAIL_IMAGE=amplop:candidate run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker compose -p probe up -d --wait
[amplop:candidate] docker tag previous amplop:local
[previous] docker compose -p probe up -d --wait --no-deps amplop
[previous] docker compose -p probe up -d --wait
EOF
}

@test "a failed previous server keeps the new worker" {
	FAIL='--wait$|--no-deps amplop' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker compose -p probe up -d --wait
[amplop:candidate] docker tag previous amplop:local
[previous] docker compose -p probe up -d --wait --no-deps amplop
EOF
}

@test "a failed rebuild keeps the new release" {
	FAIL='search rebuild' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker compose -p probe up -d --wait
[amplop:candidate] docker compose -p probe exec -T worker amplop search rebuild
EOF
}

@test "--dry-run is refused before any call" {
	run sh "$SCRIPT" --dry-run
	[ "$status" -eq 2 ]
	[ ! -s "$WORK/all" ]
}

# The script passes its own directory to every docker compose call.
@test "a checkout whose path has a space" {
	mkdir "$WORK/with space"
	cp "$SCRIPT" "$WORK/with space/upgrade.sh"
	run sh "$WORK/with space/upgrade.sh" -p probe
	[ "$status" -eq 0 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
[amplop:candidate] docker compose -p probe up -d --wait
[amplop:candidate] docker compose -p probe exec -T worker amplop search rebuild
EOF
}

# The old volume holds members' mail from an earlier database, which the
# switch must not serve beside.
@test "a failed volume removal stops before the switch" {
	VOLUMES='probe_meilisearch-1.55.0=meilisearch-1.55.0
probe_meilisearch-1.56.1=meilisearch-1.56.1' FAIL='volume rm' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
[previous] docker volume rm probe_meilisearch-1.55.0
EOF
}

@test "a configuration that names no project stops before cleanup" {
	CONFIG='services: {}' VOLUMES='probe_meilisearch-1.55.0=meilisearch-1.55.0' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
EOF
}

@test "a failed candidate tag replaces nothing" {
	FAIL='^tag amplop:candidate' run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<EOF
$before_switch
[previous] docker tag amplop:candidate amplop:local
EOF
}

# A first deploy has no previous image to go back to.
@test "a first deploy whose worker fails restores nothing" {
	echo none > "$WORK/image"
	PREVIOUS='' FAIL='--no-deps worker' FAIL_IMAGE=amplop:candidate run sh "$SCRIPT" -p probe
	[ "$status" -eq 1 ]
	calls_are <<'EOF'
AMPLOP_IMAGE=amplop:candidate [none] docker compose -p probe build
AMPLOP_IMAGE=amplop:candidate [none] docker compose -p probe run --rm migrate
[none] docker compose -p probe up -d --wait postgres meilisearch seaweedfs
[none] docker tag amplop:candidate amplop:local
[amplop:candidate] docker compose -p probe up -d --wait --no-deps worker
EOF
}
