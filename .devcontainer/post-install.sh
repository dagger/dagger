#!/usr/bin/env bash

set -exo pipefail

make install
go install -mod=readonly cuelang.org/go/cmd/cue
