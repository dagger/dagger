#!/usr/bin/env sh

set -eu

: "${AZURE_DEBUG:=0}"

__usage="Usage:
  azlogin [-s <scope>] [-r <resource>]
Flags:
  -r    set the resource for the token (defaults to 'https://management.azure.com/', if -s and -r are not set
  -s    set the scope for the token
  -h    show this help message
Environment Variables:
  AZLOGIN_RESOURCE                        set the resource
  AZLOGIN_SCOPE                           set the scope
  AZURE_TENANT_ID                         the tenant id of the servicel principal
  AAD_SERVICE_PRINCIPAL_CLIENT_ID         the client id (application id) of the service principal
  AAD_SERVICE_PRINCIPAL_CLIENT_SECRET     the client secret value of the service principal"

helptext() {
    echo >&2 "$__usage"
}

# for the generic request 'resource=https://management.azure.com/' is used
# for specific requests 'scope=<resource-scope>'. i.e.  'scope=https://vault.azure.net/.default'

# pick up the scope from environment variables
# giving precedence to flags
resource="${AZLOGIN_RESOURCE:-}"
scope="${AZLOGIN_SCOPE:-}"

while getopts r:s:h o; do
    case "$o" in
    r)
        resource="$OPTARG"
        ;;
    s)
        scope="$OPTARG"
        ;;
    h)
        helptext
        exit 1
        ;;
    [?])
        helptext
        exit 1
        ;;
    esac
done

shift $((OPTIND - 1))

# enable debug mode, if set
if [ "$AZURE_DEBUG" = "1" ]; then
    debug="1"
fi

# query params
grant_type="grant_type=client_credentials"
client_id="client_id=$AAD_SERVICE_PRINCIPAL_CLIENT_ID"
client_secret="client_secret=$AAD_SERVICE_PRINCIPAL_CLIENT_SECRET"
default_resource="resource=https://management.azure.com/"

# construct the query
query="$grant_type&$client_id&$client_secret"

# determine what resource/scope to add to the query
if [ -z "$resource" ] && [ -z "$scope" ]; then
    query="$query&$default_resource"
else
    if [ -n "$resource" ]; then
        query="$query&resource=$resource"
    fi
    if [ -n "$scope" ]; then
        query="$query&scope=$scope"
    fi
fi

token="$(mktemp)"
# shellcheck disable=SC2064
trap "rm -f $token" EXIT INT HUP

if [ "$AZURE_DEBUG" = "1" ]; then
    echo >&2 "Azure Login token Request..."
    echo >&2 "Query: $query"
fi

curl -fsSL --request POST "https://login.microsoftonline.com/$AZURE_TENANT_ID/oauth2/token" \
    --header "Content-Type: application/x-www-form-urlencoded" --data "$query" -o "$token" ${debug+-v} 1>&2

jq -r '.access_token' "$token"
