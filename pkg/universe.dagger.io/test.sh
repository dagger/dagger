#!/bin/bash

set -e

targets=(
	dagger.io/dagger
	dagger.io/dagger/engine

	./docker
	./docker/test/build

	./alpine
	./alpine/tests/simple

	./yarn
	./yarn/tests/simple

	./bash
	./python
	./git
	./nginx
	./netlify
	./netlify/test/simple

	./examples/todoapp
	./examples/todoapp/dev
	./examples/todoapp/staging
)

for t in "${targets[@]}"; do
	echo "-- $t"
	cue eval "$t" >/dev/null
done
