package helm

import (
	"dagger.io/dagger"
	"universe.dagger.io/alpha/azure/aks/helm"
)

dagger.#Plan & {
	client: {
		filesystem: "./testdata": read: contents: dagger.#FS
		env: {
			AZURE_TENANT_ID:                     string
			AAD_SERVICE_PRINCIPAL_CLIENT_ID:     string
			AAD_SERVICE_PRINCIPAL_CLIENT_SECRET: dagger.#Secret
			AKS_SUSCRIPTION_ID:                  string
			AKS_RESOURCE_GROUP:                  string
			AKS_NAME:                            string
		}
	}
	actions: test: helm.#Upgrade & {
		servicePrincipal: {
			tenantId:     client.env.AZURE_TENANT_ID
			clientId:     client.env.AAD_SERVICE_PRINCIPAL_CLIENT_ID
			clientSecret: client.env.AAD_SERVICE_PRINCIPAL_CLIENT_SECRET
		}
		cluster: {
			subscriptionId: client.env.AKS_SUSCRIPTION_ID
			resourceGroup:  client.env.AKS_RESOURCEGROUP
			name:           client.env.AKS_CLUSTER
		}
		// fetch admin credentials
		useAdminCredentials: true
		workspace:           client.filesystem."./testdata".read.contents
		name:                "redis"
		repo:                "https://charts.bitnami.com/bitnami"
		chart:               "redis"
		version:             "17.0.1"
		namespace:           "dagger-helm-upgrade-test"
		atomic:              true
		install:             true
		cleanupOnFail:       true
		debug:               true
		force:               true
		wait:                true
		timeout:             "2m"
		flags: ["--skip-crds", "--description='Dagger Test Run'"]
		values: ["values.base.yaml", "values.staging.yaml"]
		set: #"""
			architecture=standalone
			auth.enabled=false
			commonLabels.dagger\.io/set=val
			"""#
		setString: #"""
			master.podAnnotations.n=1
			master.podLabels.n=2
			"""#
	}
}
