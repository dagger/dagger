#!/usr/bin/env sh

set -eu

helptext() {
    echo >&2 "Usage:"
    echo >&2 "  azlogin [-s <scope>] [-r <resource>]"
    echo >&2 "Flags:"
    echo >&2 "  -r    set the resource for the token (defaults to 'https://management.azure.com/', if -s and -r are not set"
    echo >&2 "  -s    set the scope for the token"
    echo >&2 "  -h    show this help message"
    echo >&2 "Environment Variables:"
    echo >&2 "  AZLOGIN_RESOURCE    set the resource"
    echo >&2 "  AZLOGIN_SCOPE       set the scope"

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
        resource="resource=$OPTARG"
        ;;
    s)
        scope="scope=$OPTARG"
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
        query="$query&$resource"
    fi
    if [ -n "$scope" ]; then
        query="$query&$scope"
    fi
fi

curl -fsSL --request POST "https://login.microsoftonline.com/$AZURE_TENANT_ID/oauth2/token" \
    --header "Content-Type: application/x-www-form-urlencoded" --data "$query" |
    jq -r '.access_token'
