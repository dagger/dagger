import sys

import anyio
import dagger
from google.cloud import run_v2

GCR_SERVICE_URL = "projects/PROJECT/locations/us-central1/services/myapp"
GCR_PUBLISH_ADDRESS = "gcr.io/PROJECT/myapp"

async def main():
    # initialize Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get reference to the project directory
        source = (
            client
            .host()
            .directory(".", exclude=["node_modules", "ci"])
        )

        # get Node image
        node = (
            client
            .container(platform=dagger.Platform("linux/amd64"))
            .from_("node:16")
        )

        # mount source code directory into Node image
        # install dependencies
        # set entrypoint
        c = (
            node
            .with_mounted_directory("/src", source)
            .with_workdir("/src")
            .with_exec(["cp", "-R", ".", "/home/node"])
            .with_workdir("/home/node")
            .with_exec(["npm", "install"])
            .with_entrypoint(["npm", "start"])
        )

        # publish container to Google Container Registry
        addr = await c.publish(GCR_PUBLISH_ADDRESS)

        print(f"Published at: {addr}")

        # create Google Cloud Run client
        gcr_client = run_v2.ServicesAsyncClient()

        # define a service request
        gcr_request = run_v2.UpdateServiceRequest(
            service=run_v2.Service(
                name=GCR_SERVICE_URL,
                template=run_v2.RevisionTemplate(
                    containers=[
                        run_v2.Container(
                            image=addr,
                            ports=[
                                run_v2.ContainerPort(
                                    name="http1",
                                    container_port=1323,
                                ),
                            ],
                        ),
                    ],
                ),
            )
        )

        # update service
        gcr_operation = await gcr_client.update_service(request=gcr_request)

        # wait for service request completion
        response = await gcr_operation.result()

    print(f"Deployment for image {addr} now available at {response.uri}.")

anyio.run(main)
