The Dagger pipeline demonstrated in this tutorial (re)builds a container image of an application every time a new commit is added to the application's repository. It then publishes the container image to Google Container registry and deploys it at a public URL using Google Cloud infrastructure.

This requires the following:

- A Google Cloud service account with all necessary privileges
- A Google Cloud Run service with a public URL and defined resource/capacity/access rules
- Access to various Google Cloud APIs

:::info
This step discusses how to create a Google Cloud service account. If you already have a Google Cloud service account and key for your project, skip to [Step 2](#step-2-configure-google-cloud-apis-and-a-google-cloud-run-service).
:::

Create a Google Cloud service account, as follows:

1. Log in to the Google Cloud Console and select your project.
1. From the navigation menu, click `IAM & Admin` -> `Service Accounts`.
1. Click `Create Service Account`.
1. In the `Service account details` section, enter a string in the `Service account ID` field. This string forms the prefix of the unique service account email address.

  ![Create Google Cloud service account](/img/current/common/guides/create-gcloud-service-account-id.png)

1. Click `Create and Continue`.
1. In the `Grant this service account access to project` section, select the `Service Account Token Creator` and `Editor` roles.

  ![Create Google Cloud service account roles](/img/current/common/guides/create-gcloud-service-account-role.png)

1. Click `Continue`.
1. Click `Done`.

Once the service account is created, the Google Cloud Console displays it in the service account list, as shown below. Note the service account email address, as you will need it in the next step.

  ![List Google Cloud service accounts](/img/current/common/guides/list-gcloud-service-accounts.png)

Next, create a JSON key for the service account as follows:

1. From the navigation menu, click `IAM & Admin` -> `Service Accounts`.
1. Click the newly-created service account in the list of service accounts.
1. Click the `Keys` tab on the service account detail page.
1. Click `Add Key` -> `Create new key`.
1. Select the `JSON` key type.
1. Click `Create`.

The key is created and automatically downloaded to your local host through your browser as a JSON file.

  ![Create Google Cloud service account key](/img/current/common/guides/create-gcloud-service-account-key.png)

:::warning
Store the JSON service account key file safely as it cannot be retrieved again.
:::
