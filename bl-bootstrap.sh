#!/bin/bash

export DAGGER_WORKSPACE=local

# Create a new stack based on the 'us-east' stack maintained by the blocklayer infra team.
# Prompt interactively for remaining inputs.
dagger new -n blocklayer-dev --base-stack blocklayer-infra.dagger.cloud/us-east --input-interactive --up
