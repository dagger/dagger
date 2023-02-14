import { connect } from "@dagger.io/dagger"

import { ServicesClient } from "@google-cloud/run";

const GCR_SERVICE_URL = 'projects/PROJECT/locations/us-central1/services/myapp'
const GCR_PUBLISH_ADDRESS = 'gcr.io/PROJECT/myapp'

// initialize Dagger client
connect(async (daggerClient) => {
  // get reference to the project directory
  const source = daggerClient.host().directory(".", { exclude: ["node_modules/", "ci/"] })

  // get Node image
  const node = daggerClient.container({ platform: "linux/amd64" }).from("node:16")

  // mount cloned repository into Node image
  // install dependencies
  const c = node
    .withMountedDirectory("/src", source)
    .withWorkdir("/src")
    .withExec(["cp", "-R", ".", "/home/node"])
    .withWorkdir("/home/node")
    .withExec(["npm", "install"])
    .withEntrypoint(["npm", "start"])

  // publish container to Google Container Registry
  const gcrContainerPublishResponse = await c
    .publish(GCR_PUBLISH_ADDRESS)

  // print ref
  console.log(`Published at: ${gcrContainerPublishResponse}`)

  // initialize Google Cloud Run client
  const gcrClient = new ServicesClient();

  // define service request
  const gcrServiceUpdateRequest = {
    service: {
      name: GCR_SERVICE_URL,
      template: {
        containers: [
          {
            image: gcrContainerPublishResponse,
            ports: [
              {
                name: "http1",
                containerPort: 3000
              }
            ]
          }
        ],
      },
     }
  };

  // update service
  const [gcrServiceUpdateOperation] = await gcrClient.updateService(gcrServiceUpdateRequest);
  const [gcrServiceUpdateResponse] = await gcrServiceUpdateOperation.promise();

  // print ref
  console.log(`Deployment for image ${gcrContainerPublishResponse} now available at ${gcrServiceUpdateResponse.uri}`)

}, {LogOutput: process.stdout})
