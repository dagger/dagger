#!/bin/sh
set -o errexit
set -o xtrace

git clone --depth 1 https://github.com/sstephenson/bats
cd bats && ./install.sh "${HOME}/.local" && cd .. && rm -rf bats
