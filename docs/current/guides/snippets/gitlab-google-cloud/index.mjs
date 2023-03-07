import { connect } from "@dagger.io/dagger"

import { ServicesClient } from "@google-cloud/run";

const GCR_SERVICE_URL = "projects/PROJECT/locations/us-central1/services/myapp"
const GCR_PUBLISH_ADDRESS = "gcr.io/PROJECT/myapp"

// initialize Dagger client
connect(async (daggerClient) => {
  // get working directory on host
  const source = daggerClient
    .host()
    .directory(".", { exclude: ["node_modules/", "ci/"] })

  // build application
  const builder = daggerClient
    .container({ platform: "linux/amd64" })
    .from("golang:1.19")
    .withMountedDirectory("/src", source)
    .withWorkdir("/src")
    .withEnvVariable("CGO_ENABLED", "0")
    .withExec(["go", "build", "-o", "myapp"])

  // add binary to alpine base
  const prodImage = daggerClient
     .container({ platform: "linux/amd64" })
     .from("alpine")
     .withFile("/bin/myapp", builder.file("/src/myapp"))
     .withEntrypoint(["/bin/myapp"])

  // publish container to Google Container Registry
  const gcrContainerPublishResponse = await prodImage
    .publish(GCR_PUBLISH_ADDRESS)

  // print ref
  console.log(`Published at: ${gcrContainerPublishResponse}`)

  // create Google Cloud Run client
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
                containerPort: 1323
              }
            ]
          }
        ],
      },
     }
  };

  // update service
  const [gcrServiceUpdateOperation] = await gcrClient.updateService(gcrServiceUpdateRequest);

  // wait for service request completion
  const [gcrServiceUpdateResponse] = await gcrServiceUpdateOperation.promise();

  // print ref
  console.log(`Deployment for image ${gcrContainerPublishResponse} now available at ${gcrServiceUpdateResponse.uri}`)

}, {LogOutput: process.stdout})
