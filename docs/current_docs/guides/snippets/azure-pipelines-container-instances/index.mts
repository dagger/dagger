import { ContainerInstanceManagementClient } from "@azure/arm-containerinstance"
import { DefaultAzureCredential } from "@azure/identity"
import { connect, Client } from "@dagger.io/dagger"

// check for required variables
const vars = [
  "DOCKERHUB_USERNAME",
  "DOCKERHUB_PASSWORD",
  "AZURE_SUBSCRIPTION_ID",
  "AZURE_TENANT_ID",
  "AZURE_CLIENT_ID",
  "AZURE_CLIENT_SECRET",
]
vars.forEach((v) => {
  if (!process.env[v]) {
    console.log(`${v} variable must be set`)
    process.exit()
  }
})

// configure container group, name and location
const containerName = "my-app"
const containerGroupName = "my-app"
const containerGroupLocation = "eastus"
const resourceGroupName = "my-group"

// initialize Dagger client
connect(
  async (daggerClient: Client) => {
    // set registry password as Dagger secret
    const secret = daggerClient.setSecret(
      "password",
      process.env.DOCKERHUB_PASSWORD
    )

    // get reference to the project directory
    const source = daggerClient
      .host()
      .directory(".", { exclude: ["node_modules/", "ci/"] })

    // get Node image
    const node = daggerClient
      .container({ platform: "linux/amd64" })
      .from("node:18")

    // mount cloned repository into Node image
    // install dependencies
    // set entrypoint
    const ctr = node
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(["cp", "-R", ".", "/home/node"])
      .withWorkdir("/home/node")
      .withExec(["npm", "install"])
      .withEntrypoint(["npm", "start"])

    // publish image
    const dockerHubUsername = process.env.DOCKERHUB_USERNAME
    const address = await ctr
      .withRegistryAuth("docker.io", dockerHubUsername, secret)
      .publish(`${dockerHubUsername}/my-app`)

    // print ref
    console.log(`Published at: ${address}`)

    // initialize Azure client
    const azureClient = new ContainerInstanceManagementClient(
      new DefaultAzureCredential(),
      process.env.AZURE_SUBSCRIPTION_ID
    )

    // define deployment request
    const containerGroup = {
      containers: [
        {
          name: containerName,
          image: address,
          ports: [{ port: 3000 }],
          resources: { requests: { cpu: 1, memoryInGB: 1.5 } },
        },
      ],
      ipAddress: { type: "Public", ports: [{ port: 3000, protocol: "TCP" }] },
      osType: "Linux",
      location: containerGroupLocation,
      restartPolicy: "OnFailure",
    }

    // send request and wait until done
    const result = await azureClient.containerGroups.beginCreateOrUpdateAndWait(
      resourceGroupName,
      containerGroupName,
      containerGroup
    )

    // print ref
    console.log(
      `Deployment for image ${address} now available at http://${result.ipAddress.ip}:${result.ipAddress.ports[0].port}`
    )
  },
  { LogOutput: process.stderr }
)
