#!/bin/bash

## Create a new documentation article

function new() {
	local name
	name="$1"
	if [ -z "$name" ]; then
		echo >&2 "Usage: $0 NAME"
		return 1
	fi
	local last
	last="$(
		find . -name '[0-9]*.md' |
		sed -E -e 's/^.*\/([0-9]+)-([^\/]*)\.md/\1/' |
		sort -u -n |
		tail -n 1
	)"
	local next
	((next="$last"+1))

	local filename="$next-$name.md"
	echo "Creating $filename"
	cat <<- EOF > "$filename"
		---
		slug: /$next/$name
		displayed_sidebar: europa
		---
EOF
}

new "$@"
