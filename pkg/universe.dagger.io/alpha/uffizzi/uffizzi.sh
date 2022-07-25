#!/bin/bash

set -e -o pipefail

export UFFIZZI_PASSWORD="$(cat /run/secrets/uffizzi_password)"

export DOCKERHUB_PASSWORD="$(cat /run/secrets/dockerhub_password)"

bash -c "/root/docker-entrypoint.sh $ENTITY $VERB $UFFIZZI_COMPOSE $DEPLOYMENT_ID"