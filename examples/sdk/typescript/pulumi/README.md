# App deployment to AWS with Dagger and Pulumi

This codebase demonstrates using Dagger with [Pulumi](https://www.pulumi.com/) to deploy the [hello-dagger](https://github.com/dagger/hello-dagger) sample application to ECS on Fargate.

The codebase contains the following:

- `index.mts` contains a Dagger build.
- `/infra/ecr` contains a Pulumi program that creates an Amazon Elastic Container Registry. We use this registry to store our build artifact for deployment later.
- `/infra/ecs` contains a Pulumi program that creates a VPC, ECS cluster, and deploys an ECS on Fargate service (the hello-dagger app) fronted by an Application Load Balancer.

**IMPORTANT:** Be sure to tear down the infrastructure in the Pulumi stacks to avoid unwanted AWS charges once you are finished with this example!

```bash
cd infra/ecs && pulumi stack select dev && pulumi destroy -y && pulumi stack rm dev -y && cd -
```

```bash
cd infra/ecr && pulumi stack select dev && pulumi destroy -y && pulumi stack rm dev -y && cd -
```
