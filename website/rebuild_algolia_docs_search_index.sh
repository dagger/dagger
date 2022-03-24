#!/usr/bin/env bash
set -eo pipefail

type jq
type docker

docker run -it \
  -e API_KEY="${API_KEY:?must be set}" \
  -e APPLICATION_ID="${APPLICATION_ID:?must be set}" \
  -e "CONFIG=$(jq -r tostring < docsearch.config.json)" \
  algolia/docsearch-scraper
