#!/bin/sh
set -eu

sha256sum schema.json | cut -d' ' -f1 >schema.sha256
sha256sum /engine/dagger-engine | cut -d' ' -f1 >engine-binary.sha256
# The go toolchain stamps the commit into the binary with -X, as a string.
grep -a -q -F "$(cat engine-commit)" /engine/dagger-engine || {
	echo "dagger-engine is not stamped with commit $(cat engine-commit)" >&2
	exit 1
}
