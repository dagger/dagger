#!/usr/bin/env bash

set -ex

if ! sed --version | grep "GNU"
then
  echo "Please install GNU sed, a.k.a. gnused"
  exit 1
fi

sed --in-place --regexp-extended --expression \
  's/'"${DAGGER_VERSION_FROM:-0.2.3}"'/'"${DAGGER_VERSION_TO:-0.2.4}"'/g' \
  ./*/*.md
