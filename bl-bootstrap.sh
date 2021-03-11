#!/bin/bash

FLAVOR="$1"; shift

case "$FLAVOR" in
	base)
		# 'bl-bootstrap.sh base': create a new infra base stack
		dagger new -n blocklayer-dev --base-dir ./infra --input-interactive --up "$@"
	;;

	dev)
		# 'bl-bootstrap.sh dev': create a new dev stack
		# Create a new stack based on the 'us-east' stack maintained by the blocklayer infra team.
		# Prompt interactively for remaining inputs.
		dagger new -n blocklayer-dev --base-stack blocklayer-infra.dagger.cloud/us-east --input-interactive --up "$@"
	;;

esac


