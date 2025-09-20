#!/bin/bash --noprofile --norc -e -o pipefail

cd $(mktemp -d)
git clone https://github.com/dagger/dagger.git . --revision=$DAGGER_REF

./hack/build

export _EXPERIMENTAL_DAGGER_CLI_BIN=$(realpath ./bin/dagger)
echo "_EXPERIMENTAL_DAGGER_CLI_BIN=$_EXPERIMENTAL_DAGGER_CLI_BIN" >>"${GITHUB_ENV}"
