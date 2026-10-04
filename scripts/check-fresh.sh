#!/bin/sh
# Fails if a command would change generated code, i.e. the committed copy
# is out of date. Leaves the code as it was either way. Example:
#   scripts/check-fresh.sh web/src/api/generated make generate-web
set -eu

path=$1
shift

# Save a copy, and put it back however this script ends, e.g. Ctrl+C.
saved=$(mktemp -d)
copy="$saved/$(basename "$path")"
cp -R "$path" "$copy"

restore() {
	rm -rf "$path"
	cp -R "$copy" "$path"
	rm -rf "$saved"
}
trap restore EXIT
trap 'exit 130' INT TERM

# Generators print progress; show it only if the command fails.
if ! "$@" > "$saved/log" 2>&1; then
	cat "$saved/log" >&2
	exit 1
fi

# -N shows a file the command added or deleted as a whole-file change.
if ! diff -ruN "$copy" "$path"; then
	echo "$path is out of date. Update it with: $*" >&2
	exit 1
fi
