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
	next=$(LC_ALL=C tr -dc '0-9' < /dev/urandom | head -c6)

	local filename="$next-$name.md"
	echo "Creating $filename"
	cat <<- EOF > "$filename"
		---
		slug: /$next/$name
				---
EOF
}

new "$@"
