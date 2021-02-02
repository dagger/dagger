# Dagger: the devops superglu, by the creators of docker

Dagger is a scripting environment for automating the delivery of your application to the cloud,
from code to production.

If you are familiar with PaaS services like Heroku, Firebase or Openshift:
Dagger makes it easy to create a custom PaaS that is perfectly tailored to each application,
instead of forcing it into a rigid, homogeneous mold. The Dagger philosophy
is that the platform should adapt to the application, not the other way around.

This means that, unlike traditional PaaS, you can use Dagger with all your applications,
*without having to change your tooling, software stack or infrastructure*.

TODO:
*[... principles: productivity + compat]*
*[.. do out-of-the-box things that would require months of work otherwise]*
*[.. alternative to the dilemna: paas or build your own?]*

## Warning: alpha software

Please note that Dagger is *alpha-quality software*. This means it has many bugs,
the user interface is minimal, and it may change in incompatible ways at any time.

If you are still willing to try it, thank you! We appreciate your help.
Please do not hesitate to ask questions, report bugs, or share feedback,
either in a github or in the discord chatroom.


## Platform as code

The most important feature of Dagger is that it is *programmable*.

Thanks to its powerful scripting environment and growing catalog of reusable components,
anyone with basic programming knowledge can assemble a custom platform for their application,
and quickly start automating repetitive and complex tasks, so the entire team can spend
less time getting deployment to work, and more time developing.

Whether you're a seasoned SRE building a custom PAAS for your organization, a hobbyist on a fun
over-engineered week-end project, or a developer trying to setup CICD because, well, someone has to do it..
There's a whole community of fellow automation enthusiasts ready to help you write your first script.


## Usage examples

A few examples of how Dagger is used in the wild:

- Use AWS Elastic Container Service to deploy the new API, while continuing to deploy the main app on Heroku.
- Deploy lightweight staging environments on-demand for QA, integration testing or product demos.
- Run integration tests on a live production-like deployment, automatically, for each pull request.
- Deploy the same app on Netlify for testing, and on Kubernetes for production
- Replace a 500-line deploy.sh with a 10-line configuration file
- Control sprawl of serverless functions on AWS, Google Cloud, Cloudflare, Netlify etc. by gradually
    moving them to a generic interface, and switching backend at will.
- When the ML team uploads a new model to their S3 bucket, automatically incorporate it into staging
    deployments, but not into production until manual confirmation!
- Rotate database credentials, and automatically re-deploy all staging environments with the new credentials.
- Allocate cool auto-generated URLs to development instances, and automatically configure your DNS,
	load-balancer and SSL certificate manager to route traffic to them.
- Orchestrate application deployment across 2 infrastructure siloes, one managed with CloudFormation, the other with Terraform.
- Migrate from Helm to Kustomize, without disrupting next week's big release. 


## Getting started

1. Build the `dagger` command-line tool. You will need [Go](https://golang.org) version 1.13 or later.

```
$ make
```

2. Copy the `dagger` tool to a location listed in your `$PATH`. For example, to copy it to `/usr/local/bin`:

```
$ cp ./cmd/dagger/dagger /usr/local/bin
```

3. Run [buildkitd](https://github.com/moby/buildkit) on your local machine. The simplest way to do this is using [Docker](https://docker.com): `docker run -d --name buildkitd --privileged moby/buildkit:latest`

On a machine with Docker installed, run:

```
$ docker run -d --name buildkitd --privileged moby/buildkit:latest
```

4. Compute a test configuration

Currently `dagger` can only do one thing: compute a configuration with optional inputs, and print the result.

If you are confused by how to use this for your application, that is normal: `dagger compute` is a low-level command
which exposes the naked plumbing of the Dagger engine. In future versions, more user-friendly commands will be available
which hide the complexity of `dagger compute` (but it will always be available to power users, of course!).

Here is an example command, using an example configuration:

```
$ dagger compute ./examples/simple --input-string www.host=mysuperapp.com --input-dir www.source=.
```


## Custom buildkit setup

Dagger can be configured to use an existing buildkit daemon, running either locally or remotely. This can be done using two environment variables: `BUILDKIT_HOST` and `DOCKER_HOST`.

To use a buildkit daemon listening on TCP port `1234` on localhost:

```
$ export BUILDKIT_HOST=tcp://localhost:1234
```

To use a buildkit daemon running in a container named "super-buildkit" on the local docker host:

```
$ export BUILDKIT_HOST=docker-container://super-buildkit
```

To use a buildkit daemon running on a remote docker host (be careful to properly secure remotely accessible docker hosts!)

```
$ export BUILDKIT_HOST=docker-container://super-buildkit
$ export DOCKER_HOST=tcp://my-remote-docker-host:2376
```


## Comparison to other automation platforms


### CICD

Github, Gitlab, Jenkins, Spinnaker, Tekton

### Build systems

Bazel, Nix, Skopeo

### Infrastructure automation

Terraform, Pulumi, Ansible

### Traditional scripting

Bash, Make, Python

### PaaS

Heroku, Elastic Beanstalk, Cloud Foundry, Openshift

### Kubernetes management

Kustomize, Helm, jsonnet

### Gitops 

Flux, ... 


