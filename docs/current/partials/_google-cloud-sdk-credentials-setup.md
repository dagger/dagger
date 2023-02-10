1. Configure Docker credentials for Google Container Registry on the local host using the following commands. Replace the SERVICE-ACCOUNT-ID placeholder with the service account email address created in Step 1, and the SERVICE-ACCOUNT-KEY-FILE placeholder with the location of the service account JSON key file downloaded in Step 1.

  ```shell
  gcloud auth activate-service-account SERVICE-ACCOUNT-ID --key-file=SERVICE-ACCOUNT-KEY-FILE
  gcloud auth configure-docker
  ```

  :::info
  This step is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
  :::

1. Set the `GOOGLE_APPLICATION_CREDENTIALS` environment variable to the location of the service account JSON key file, replacing the SERVICE-ACCOUNT-KEY-FILE placeholder in the following command. This variable is used by the Google Cloud Run client library during the client authentication process.

  ```shell
  export GOOGLE_APPLICATION_CREDENTIALS=SERVICE-ACCOUNT-KEY-FILE
  ```
