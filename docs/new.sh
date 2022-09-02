#!/bin/bash

## Create a new documentation article

function new() {
	local name
	name="$1"
	if [ -z "$name" ]; then
		echo >&2 "Usage: $0 NAME"
		return 1
	fi
	local next
	next=$(LC_ALL=C tr -dc 'a-z0-9' < /dev/urandom | head -c5)

	local filename="$next-$name.md"
	echo "Creating $filename"
	cat <<- EOF > "$filename"
		---
		slug: /$next/$name
		displayed_sidebar: '0.3'
		---
EOF
}

new "$@"
