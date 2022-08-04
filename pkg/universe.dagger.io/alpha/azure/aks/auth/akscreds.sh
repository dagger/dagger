#!/usr/bin/env sh

set -eu

: "${KUBECONFIG:=$HOME/.kube/config}"

__usage="Usage:
  aks-get-credentials [-a] [-f format] [-o path]
Flags:
  -a    fetch admin credentials instead of user credentials
  -f    format of the kubeconfig. Possible values: 'azure', 'exec' . Defaults to 'exec'
  -o    write the kubeconfig to the specified path. Writes to stdout, if not specified or set to '-'
  -h    show this help message
Environment Variables:
  AKS_SUSCRIPTION_ID                      the subscription id of the cluster
  AKS_RESOURCE_GROUP                      the resource group of the cluster
  AKS_NAME                                the name of the cluster
  AZURE_TENANT_ID                         the tenant id of the servicel principal
  AAD_SERVICE_PRINCIPAL_CLIENT_ID         the client id (application id) of the service principal
  AAD_SERVICE_PRINCIPAL_CLIENT_SECRET     the client secret value of the service principal"

helptext() {
    echo >&2 "$__usage"
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
    resource_group="resourceGroups/$AKS_RESOURCE_GROUP"
    provider="providers/Microsoft.ContainerService"
    resource="managedClusters/$AKS_NAME"

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
    config="$(mktemp)"
    touch "$config"
    # shellcheck disable=SC2064
    trap "rm -f $config" EXIT INT HUP

    curl -fsSL --request POST --url "$url" \
        --header "Authorization: Bearer $access_token" \
        --header "Content-type: application/json" \
        --header "Content-Length: 0" >"$config"

    jq -r '.kubeconfigs[0].value | @base64d' "$config"
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
