#!/bin/bash

mkdir -p test

cat > test/source.cue << EOF
package test

import (
  "github.com/tjovicic/gcpcloudrun"
)

run: gcpcloudrun.#Run
EOF

dagger new staging -p ./test
