# Deploy an application to GCP Cloud Run

This example shows how to deploy an application to GCP Cloud Run. Read the deployment [plan](https://github.com/dagger/dagger/tree/main/examples/cloud-run-app)

NOTE: this example requires an EKS cluster to allow authentication with your AWS credentials; but can easily be adapter to deploy to any Kubernetes cluster.

Components:

- [Cloud Run](https://cloud.google.com/run)

How to run:

1. Initialize a new workspace

   ```sh
   cd ./cloud-run-app
   dagger init
   ```

2. Create a new environment

   ```sh
   dagger new cloud-run-app
   cp *.cue ./.dagger/env/cloud-run-app/plan/
   ```

3. Configure the Cloud Run service

   ```sh
   dagger input text serviceName MY_APP_NAME
   dagger input text region MY_GCP_REGION
   dagger input text image MY_GCR_IMAGE_NAME

   dagger input text gcpConfig.project MY_GCP_PROJECT
   dagger input secret gcpConfig.serviceKey -f MY_GCP_SERVICE_KEY_FILE
   ```

4. Deploy!

   ```sh
   dagger up
   ```
