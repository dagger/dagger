#!/bin/bash --noprofile --norc -e -o pipefail

GITHUB_ENV="${GITHUB_ENV:=github.env}"
DAGGER_SOURCE="${DAGGER_SOURCE:=.}"

if [ ! -d "$DAGGER_SOURCE" ]; then
    dagger core \
        directory \
        with-directory --path=. --directory="$DAGGER_SOURCE" \
        export --path=dagger-source
    DAGGER_SOURCE=./dagger-source
fi

echo "::group::Starting dev engine"

if ! [[ -x "$(command -v docker)" ]]; then
    echo "docker is not installed"
    exit 1
fi
if ! [[ -x "$(command -v dagger)" ]]; then
    echo "dagger is not installed"
    exit 1
fi

$DAGGER_SOURCE/hack/build
export PATH=$(realpath ./bin):$PATH
echo "PATH=$PATH" >>"${GITHUB_ENV}"

export _EXPERIMENTAL_DAGGER_CLI_BIN=$(which dagger)
echo "_EXPERIMENTAL_DAGGER_CLI_BIN=$_EXPERIMENTAL_DAGGER_CLI_BIN" >>"${GITHUB_ENV}"

echo "USE_DEV_ENGINE=y" >> "${GITHUB_ENV}"

echo "::endgroup::"
