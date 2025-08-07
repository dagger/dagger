#!/bin/bash --noprofile --norc -e -o pipefail

cd $(mktemp -d)
dagger shell -M -c "git https://github.com/dagger/dagger.git | ref $DAGGER_REF | tree | export ."

./hack/build

export _EXPERIMENTAL_DAGGER_CLI_BIN=$(realpath ./bin/dagger)
echo "_EXPERIMENTAL_DAGGER_CLI_BIN=$_EXPERIMENTAL_DAGGER_CLI_BIN" >>"${GITHUB_ENV}"
