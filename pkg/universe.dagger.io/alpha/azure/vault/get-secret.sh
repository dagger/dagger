#!/usr/bin/env sh
set -eu

# enable debug mode, if set
if [ "$AZURE_DEBUG" = "1" ]; then
    debug="1"
fi

secret_name="$1"
secret_version="${2:-latest}"
api_version="7.3"

url="$AZURE_VAULT_URI/secrets/$secret_name"
if [ "$secret_version" != "latest" ]; then
    url="$url/$secret_version"
fi

raw_secret="$(mktemp)"
# shellcheck disable=SC2064
trap "rm -f $raw_secret" EXIT INT HUP

curl --fail --silent --show-error --location ${debug+-v} \
    --header "Authorization: Bearer $AAD_ACCESS_TOKEN" \
    --url "$url?api-version=$api_version" \
    --output "$raw_secret"

jq -r '.value' "$raw_secret"
