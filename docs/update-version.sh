#!/usr/bin/env bash

set -ex

if ! sed --version | grep "GNU"
then
  echo "Please install GNU sed, a.k.a. gnused"
  exit 1
fi

sed --in-place --regexp-extended --expression \
  's/v'"${DAGGER_VERSION_FROM:-0.2.0}"'/v'"${DAGGER_VERSION_TO:-0.2.3}"'/g' \
  ./*/*.md
