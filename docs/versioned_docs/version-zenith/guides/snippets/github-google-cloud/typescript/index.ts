import { dag, Container, Directory, object, func } from "@dagger.io/dagger"
import { ServicesClient } from "@google-cloud/run"

@object
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {

  // build a container
  @func
  build(source: Directory): Container {
    return dag.container().from("node:21")
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withExec(["cp", "-R", ".", "/home/node"])
      .withWorkdir("/home/node")
      .withExec(["npm", "install"])
      .withEntrypoint(["npm", "start"])
  }

  // publish an image
  // example: dagger call publish --source . --registry REGISTRY/myapp --credential env:GOOGLE_JSON
  @func
  async publish(source: Directory, registry: string, credential: Secret): Promise<string> {
    const split = registry.split("/")
    return await this.build(source)
      .withRegistryAuth(split[0], "_json_key", credential)
      .publish(registry)
  }

  // deploy an image to Google Cloud Run
  // example: dagger call publish --source . --registry REGISTRY/myapp --service SERVICE --credential env:GOOGLE_JSON
  @func
  async deploy(source: Directory, service: string, registry: string, credential: Secret): Promise<string> {

    // get JSON secret
    const json = JSON.parse(await credential.plaintext())
    const gcrClient = new ServicesClient({credentials: json})

    // publish image
    const gcrContainerPublishResponse = await this.publish(source, registry, credential)

    // define service request
    const gcrServiceUpdateRequest = {
      service: {
        name: service,
        template: {
          containers: [
            {
              image: gcrContainerPublishResponse,
              ports: [
                {
                  name: "http1",
                  containerPort: 3000,
                },
              ],
            },
          ],
        },
      },
    }

    // update service
    const [gcrServiceUpdateOperation] = await gcrClient.updateService(
      gcrServiceUpdateRequest
    )

    // wait for service request completion
    const [gcrServiceUpdateResponse] = await gcrServiceUpdateOperation.promise()

    // return service URL
    return gcrServiceUpdateResponse.uri
  }
}
