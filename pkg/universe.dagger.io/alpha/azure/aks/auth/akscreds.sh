#!/usr/bin/env sh

set -eu

: "${KUBECONFIG:=$HOME/.kube/config}"

helptext() {
    echo >&2 "Usage:"
    echo >&2 "  aks-get-credentials [-a] [-f format] [-o path]"
    echo >&2 "Flags:"
    echo >&2 "  -a    fetch admin credentials instead of user credentials"
    echo >&2 "  -f    format of the kubeconfig. Possible values: 'azure', 'exec' . Defaults to 'exec'"
    echo >&2 "  -o    write the kubeconfig to the specified path. Writes to stdout, if not specified or set to '-'"
    echo >&2 "  -h    show this help message"
}

# write the file to output.
# - means stdout
output="-"

# the endpoint determines if the admin or
# user config should be fetched
endpoint_admin="listClusterAdminCredential"
endpoint_user="listClusterUserCredential"
endpoint="$endpoint_user"

# the exec format is meant to be used with kubelogin
# https://github.com/Azure/kubelogin
format="exec"

while getopts af:o:h opts; do
    case "$opts" in
    a)
        endpoint="$endpoint_admin"
        ;;
    f)
        format="$OPTARG"
        ;;
    o)
        output="$OPTARG"
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

get_credentials() {

    # the bearer token is the second arg
    access_token="$2"

    #
    # URL

    base="https://management.azure.com"

    # key value pair url parts
    subscription="subscriptions/$AKS_SUSCRIPTION_ID"
    resource_group="resourceGroups/$AKS_RESOURCEGROUP"
    provider="providers/Microsoft.ContainerService"
    resource="managedClusters/$AKS_CLUSTER"

    # the endpoint is the first arg
    credentials_endpoint="$1"

    #
    # Query Params

    api_version="2022-04-01"
    format_param="$format"

    #
    # Work

    # construct the url
    url="$base/$subscription/$resource_group/$provider/$resource/$credentials_endpoint?api-version=$api_version&format=$format_param"

    # make the api call to get the kubeconfig
    curl -fsSL --request POST --url "$url" \
        --header "Authorization: Bearer $access_token" \
        --header "Content-type: application/json" \
        --header "Content-Length: 0" |
        jq -r '.kubeconfigs[0].value | @base64d'
}

#
# get the token
token="$(azlogin)"

# get the kubeconfig
if [ "$output" = "-" ]; then
    get_credentials "$endpoint" "$token"
else
    get_credentials "$endpoint" "$token" >"$output"
    chmod 600 "$output"
fi
