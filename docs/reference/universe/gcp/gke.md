---
sidebar_label: gke
---

# dagger.io/gcp/gke

Google Kubernetes Engine

## #KubeConfig

KubeConfig config outputs a valid kube-auth-config for kubectl client

### #KubeConfig Inputs

| Name                  | Type                      | Description        |
| -------------         |:-------------:            |:-------------:     |
|*config.region*        | `string`                  |GCP region          |
|*config.project*       | `string`                  |GCP project         |
|*config.serviceKey*    | `dagger.#Secret`          |GCP service key     |
|*clusterName*          | `string`                  |GKE cluster name    |
|*version*              | `*"v1.19.9" \| string`    |Kubectl version     |

### #KubeConfig Outputs

| Name             | Type              | Description                                           |
| -------------    |:-------------:    |:-------------:                                        |
|*kubeconfig*      | `string`          |kubeconfig is the generated kube configuration file    |
