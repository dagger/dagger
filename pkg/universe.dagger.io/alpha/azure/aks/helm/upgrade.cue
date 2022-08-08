package helm

import (
	azauth "universe.dagger.io/alpha/azure/auth"
	"universe.dagger.io/alpha/azure/aks/auth"
	"universe.dagger.io/alpha/kubernetes/helm"
)

#Upgrade: {
	// version of the kubelogin plugin
	// only applicable of admin credentials
	// are not used
	kubeloginVersion: *"0.0.20" | string

	// get the kubeconfig with admin credentials
	useAdminCredentials: *false | true

	// the service principal to use
	servicePrincipal: azauth.#ServicePrincipal

	// the cluster to connect to
	cluster: auth.#Cluster

	// handle the kubeconfig
	_kubeConfig: auth.#GetCredentials & {
		admin:              useAdminCredentials
		"servicePrincipal": servicePrincipal
		"cluster":          cluster
	}

	// embed the upgrade action
	helm.#Upgrade & {
		kubeconfig: _kubeConfig.output

		// for user credentials, the kubelogin go binrary
		// should be used since azure login is deprecated
		if !useAdminCredentials {
			_klimg: #KubeloginImage & {
				"kubeloginVersion": kubeloginVersion
			}
			image: _klimg.output
			env: {
				AZURE_TENANT_ID:                     servicePrincipal.tenantId
				AAD_SERVICE_PRINCIPAL_CLIENT_ID:     servicePrincipal.clientId
				AAD_SERVICE_PRINCIPAL_CLIENT_SECRET: servicePrincipal.clientSecret
			}
		}

	}
}
