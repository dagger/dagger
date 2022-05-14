# GCP

This package provides declarations to interact with the Google Cloud Platform.

The following are available:
- [Gcloud Tool](#gcloud)
- [Login](#login)
- [Config](#config)
- [Serverless Cloud Function](#serverless-cloud-function)


## Gcloud

This is the gcloud utility tool. It allows to interact with gcloud and provides a docker image that can be used for multiple actions and it can be really usefull for multiple deployements since the login is required only once.

It is available in [gcloud.cue](./gcloud.cue)

## Login

The login allows users to authenticate themselves to further deploy things on Google Cloud Platform.
The login is meant to be done with service keys since it autheticates as an app would do.

It is available in [gcr/gcr.cue](./gcr/gcr.cue)

## Config

This allows to configure things used for every deployement on GCP, these are the region, zone, project and service key.
It is pretty easy to use and is meant to be used with the [Login](#login) part.

It is available in [gcp.cue](./gcp.cue)

## Serverless Cloud Function

You can deploy serverless cloud function with this declaration.
You only need the name, the source folder, the runtime name and version and you should be ready to deploy your first cloud function

It is available in [function/function.cue](./function/function.cue)

## Examples

For examples of how to use this package, you can check the examples [right here](./test/README.md)