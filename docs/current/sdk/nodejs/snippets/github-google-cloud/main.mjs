import { connect } from "@dagger.io/dagger"

import { ServicesClient } from "@google-cloud/run";

const GCR_SERVICE_URL = 'projects/PROJECT/locations/us-central1/services/myapp'
const GCR_PUBLISH_ADDRESS = 'gcr.io/PROJECT/myapp'

// initialize Dagger client
connect(async (daggerClient) => {
  // get reference to the project directory
  const source = await daggerClient.host().directory(".", { exclude: ["node_modules/", "ci/"] })

  // get Node image
  const node = await daggerClient.container().from("node:16")

  // mount cloned repository into Node image
  // install dependencies
  const c = await node
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
  console.log(`Published at: ${gcrContainerPublishResponse.publish}`)

  // initialize Google Cloud Run client
  const gcrClient = new ServicesClient();

  // define service
  const gcrService = {
    name: GCR_SERVICE_URL,
    template: {
      containers: [
        {
          image: `${gcr.publish}`,
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

  // update service
  const [gcrServiceUpdateRequest] = await gcrClient.updateService({gcrService});
  const [gcrServiceUpdateResponse] = await gcrServiceUpdateRequest.promise();

  // print ref
  console.log(`Deployment for image ${gcrContainerPublishResponse.publish} now available at ${gcrServiceUpdateResponse.uri}`)

}, {LogOutput: process.stdout})
