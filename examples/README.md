# Dagger Examples

All example commands should be executed in the `examples/` directory
in an up-to-date checkout of the [dagger repository](https://github.com/dagger/dagger).

## react: Deploy a simple React application

This example shows how to deploy an example React Application.

Components:

- [Netlify](https://netlify.com) for application hosting
- [Yarn](https://yarnpkg.com) for building
- [Github](https://github.com) for source code hosting
- [React-Todo-App](https://github.com/kabirbaidhya/react-todo-app) by Kabir Baidhya as a sample application.

1. Change the current directory to the example deployment plan

```sh
cd ./react
```

2. Create a new deployment from the plan

```sh
dagger new
```

3. Configure the deployment with your Netlify access token.
You can create new tokens from the [Netlify dashboard](https://app.netlify.com/user/applications/personal).

```sh
dagger input text www.account.token MY_TOKEN
```

*NOTE: there is a dedicated command for encrypted secret inputs, but it is
not yet implemented. Coming soon!*

4. Deploy!

```sh
dagger up
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
