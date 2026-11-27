#!/bin/sh
set -eu

commit=$(cat engine-commit)
case $commit in
*[!0-9a-f]*) commit= ;;
esac
[ ${#commit} -eq 40 ] || {
	echo "engine commit $(cat engine-commit) is not a full lowercase commit SHA" >&2
	exit 1
}

sha256sum schema.json | cut -d' ' -f1 >schema.sha256
sha256sum /engine/dagger-engine | cut -d' ' -f1 >engine-binary.sha256
# The go toolchain stamps the commit into the binary with -X, as a string.
grep -a -q -F "$commit" /engine/dagger-engine || {
	echo "dagger-engine is not stamped with commit $commit" >&2
	exit 1
}
