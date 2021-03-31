# Dagger Examples

All example commands should be executed in the `examples/` directory
in an up-to-date checkout of the [dagger repository](https://github.com/dagger/dagger).

## react-netlify: Deploy a React Web app to Netlify

This example shows how to deploy an example React Application to Netlify,
using Dagger.

1. Create a new deployment with the react-netlify deployment plan.

```sh
dagger new --name example-one --base-dir ./react-netlify
```

2. Configure the deployment with your Netlify access token.
You can create new tokens from the [Netlify dashboard](https://app.netlify.com/user/applications/personal).


```sh
dagger -d example-one input secret todoApp.account.token MY_TOKEN
```

3. You can deploy updates at any time

```sh
dagger -d example-one up
```

## aws-eks: Kubernetes on AWS (EKS)

This example provisions a Kubernetes (EKS) cluster on AWS using Cloudformation,
it also outputs the new generated kubeconfig for the `kubectl` client.

How to run:

```sh
dagger compute ./aws-eks \
    --input-string awsConfig.accessKey="MY_AWS_ACCESS_KEY" \
    --input-string awsConfig.secretKey="MY_AWS_SECRET_KEY" \
    | jq -j '.kubeconfig.kubeconfig' > kubeconfig
```

## aws-monitoring: HTTP Monitoring on AWS

This example implements a full HTTP(s) Monitoring solution on AWS using
Cloudformation and Cloudwatch Synthetics.

How to run:

```sh
dagger compute ./aws-monitoring \
    --input-string awsConfig.accessKey="MY_AWS_ACCESS_KEY" \
    --input-string awsConfig.secretKey="MY_AWS_SECRET_KEY" \
```

## kubernetes: Deploy to an existing Kubernetes cluster

This example shows two different ways for deploying to an existing Kubernetes
(EKS) cluster: a simple deployment spec (written in Cue), and a local helm
chart.

How to run:

```sh
dagger compute ./kubernetes \
    --input-string awsConfig.accessKey="MY_AWS_ACCESS_KEY" \
    --input-string awsConfig.secretKey="MY_AWS_SECRET_KEY" \
    --input-dir helmChart.chart=./kubernetes/testdata/mychart
```
