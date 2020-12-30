# dagger: the devops superglu, by the creators of docker

Dagger is an automation platform that lets software teams bind all their tools and infrastructure together into a unified supply chain.

Thanks to its vast catalog of adapters, developed in the open and curated for safety and quality,
you can drop Dagger into an existing stack *without changing it*, and immediately start automating the most repetitive and complex tasks.

And most importantly, Dagger is *programmable*. Thanks to a powerful scripting environment,
anyone with basic programming knowledge can extend Dagger with their own custom adapters and workflows.
Whether you're a seasoned SRE building a custom PAAS for your organization, a hobbyist on a fun
over-engineered week-end project, or a developer trying to setup CICD because, well, someone has to do it..
There's a whole community of fellow automation enthusiasts ready to help you write your first Dagger script.

## Usage examples

A few examples of how Dagger is used in the wild:

- Deploy a new API to AWS Elastic Container Service while continuing to deploy the main app on Heroku
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
