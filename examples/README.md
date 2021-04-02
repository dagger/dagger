# Dagger Examples

All example commands should be executed in the `examples/` directory
in an up-to-date checkout of the [dagger repository](https://github.com/dagger/dagger).

## Summary

- [Dagger Examples](#dagger-examples)
- [Summary](#summary)
- [Deploy a simple React application](#deploy-a-simple-react-application)
- [Provision a Kubernetes cluster on AWS](#provision-a-kubernetes-cluster-on-aws)
- [Add HTTP monitoring to your application](#add-http-monitoring-to-your-application)
- [Deploy an application to your Kubernetes cluster](#deploy-an-application-to-your-kubernetes-cluster)
- [Deploy an application to your kind kubernetes cluster](#Deploy-an-application-to-your-kind-kubernetes-cluster)

## Deploy a simple React application

This example shows how to deploy an example React Application. [Read the deployment plan](https://github.com/dagger/dagger/tree/main/examples/react)

Audience: Javascript developers looking to deploy their application.

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


## Provision a Kubernetes cluster on AWS

This example shows how to provision a new Kubernetes cluster on AWS, and configure your `kubectl` client to use it. [Read the deployment plan](https://github.com/dagger/dagger/tree/main/examples/kubernetes-aws)

Audience: infrastructure teams looking to provisioning kubernetes clusters as part of automated CICD pipelines.

Components:

- [Amazon EKS](https://aws.amazon.com/eks) for Kubernetes hosting
- [Amazon CloudFormation](https://aws.amazon.com/cloudformation) for infrastructure provisioning
- [Kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl) as kubernetes client

1. Change the current directory to the example deployment plan

```sh
cd ./kubernetes-aws
```

2. Create a new deployment from the plan

```sh
dagger new
```

3. Configure the deployment with your AWS credentials

```sh
dagger input text awsConfig.accessKey MY_AWS_ACCESS_KEY
```

```sh
dagger input text awsConfig.secretKey MY_AWS_SECRET_KEY
```


4. Deploy!

```sh
dagger up
```

5. Export the generated kubectl config

```sh
dagger query kubeconfig.kubeconfig | jq . > kubeconfig
```

## Add HTTP monitoring to your application

This example shows how to implement a robust HTTP(s) monitoring service on top of AWS. [Read the deployment plan](https://github.com/dagger/dagger/tree/main/examples/monitoring).

Audience: application team looking to improve the reliability of their application.

Components:

- [Amazon Cloudwatch Synthetics](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch_Synthetics_Canaries.html) for hosting the monitoring scripts
- [Amazon CloudFormation](https://aws.amazon.com/cloudformation) for infrastructure provisioning


1. Change the current directory to the example deployment plan

```sh
cd ./monitoring
```

2. Create a new deployment from the plan

```sh
dagger new
```

3. Configure the deployment with your AWS credentials

```sh
dagger input text awsConfig.accessKey MY_AWS_ACCESS_KEY
```

```sh
dagger input text awsConfig.secretKey MY_AWS_SECRET_KEY
```

4. Configure the monitoring parameters

```sh
dagger input text website https://MYWEBSITE.TLD
```

```sh
dagger input text email my_email@my_domain.tld
```

5. Deploy!

```sh
dagger up
```


## Deploy an application to your Kubernetes cluster

This example shows two different ways to deploy an application to an existing Kubernetes cluster: with and without a Helm chart. [Read the deployment plan](https://github.com/dagger/dagger/tree/main/examples/kubernetes-app)

NOTE: this example requires an EKS cluster to allow authentication with your AWS credentials; but can easily be adapter to deploy to any Kubernetes cluster.

Components:

- [Amazon EKS](https://aws.amazon.com/eks) for Kubernetes hosting
- [Kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl) as kubernetes client
- [Helm](https://helm.sh) to manage kubernetes configuration (optional)

How to run:


1. Change the current directory to the example deployment plan

```sh
cd ./kubernetes-app
```

2. Create a new deployment from the plan

```sh
dagger new
```

3. Configure the deployment with your AWS credentials

```sh
dagger input text awsConfig.accessKey MY_AWS_ACCESS_KEY
```

```sh
dagger input text awsConfig.secretKey MY_AWS_SECRET_KEY
```

4. Configure the EKS cluster to deploy to

Note: if you have run the `kubernetes-aws` example, you may skip this step.

```sh
dagger input text cluster.clusterName MY_CLUSTER_NAME
```

5. Load the Helm chart

```sh
dagger input dir helmChart.chart=./kubernetes-app/testdata/mychart
```

6. Deploy!

```sh
dagger up
```

## Deploy an application to your kind kubernetes cluster

This example show you how to deploy kubernetes application with dagger in a local kubernetes cluster powered by kind.
[Read the deployment plan]() <!-- //TODO -->

Audience: developers and devops looking to provisioning local kubernetes clusters.

Components:
 - [Kind](https://kind.sigs.k8s.io/) for Kubernetes local cluster
 - [Kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl) as kubernetes client

How to run :

1. Change the current directory to the example deployment plan

```sh
cd ./kubernetes-kind
```

2. Run a new kind cluster

```sh
kind create cluster
```

3. Create a new deployment from the plan

```sh
dagger new
```

4. Configure the kind cluster to deploy to
```sh
dagger input dir kubeDirectory=/home/$USER/.kube
```

5. Deploy
```sh 
dagger up
```
