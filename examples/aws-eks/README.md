# Kubernetes on AWS (EKS)

This example creates a new EKS cluster and outputs its corresponding kubeconfig

## How to run

```sh
dagger compute . \
    --input-string awsConfig.accessKey="MY_AWS_ACCESS_KEY" \
    --input-string awsConfig.secretKey="MY_AWS_SECRET_KEY" \
    | jq -j '.kubeconfig.kubeconfig' > kubeconfig
```
