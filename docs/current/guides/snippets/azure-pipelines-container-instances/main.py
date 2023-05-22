import sys
import os

import anyio
import dagger
from azure.identity import DefaultAzureCredential
from azure.mgmt.containerinstance import ContainerInstanceManagementClient

# configure container group, name and location
container_name = "my-app";
container_group_name = "my-app";
container_group_location = "eastus";
resource_group_name = "my-group";

async def main():

    # check for required variables in host environment
    for var in ["DOCKERHUB_USERNAME", "DOCKERHUB_PASSWORD", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"]:
        if var not in os.environ:
            raise EnvironmentError('"%s" environment variable must be set' % var)

    # initialize Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as dagger_client:

        # set registry password as Dagger secret
        secret = dagger_client.set_secret("password", os.environ.get("DOCKERHUB_PASSWORD"));

        # get reference to the project directory
        source = (
            dagger_client
            .host()
            .directory(".", exclude=["node_modules", "ci"])
        )

        # get Node image
        node = (
            dagger_client
            .container(platform=dagger.Platform("linux/amd64"))
            .from_("node:18")
        )

        # mount source code directory into Node image
        # install dependencies
        # set entrypoint
        c = (
            node
            .with_directory("/src", source)
            .with_workdir("/src")
            .with_exec(["cp", "-R", ".", "/home/node"])
            .with_workdir("/home/node")
            .with_exec(["npm", "install"])
            .with_entrypoint(["npm", "start"])
        )

        # publish image
        docker_hub_username = os.environ.get("DOCKERHUB_USERNAME")
        addr = await (
          c
          .with_registry_auth("docker.io", docker_hub_username, secret)
          .publish(f"{docker_hub_username}/my-app")
        )

        # print ref
        print(f"Published at: {addr}")

        # initialize Azure client
        azure_client = ContainerInstanceManagementClient(
          credential=DefaultAzureCredential(), subscription_id=os.environ.get("AZURE_SUBSCRIPTION_ID"),
        )

        # define deployment request
        container_group = {
          "containers": [
            {
              "name": container_name,
              "image": addr,
              "ports": [{ "port": "3000" }],
              "resources": { "requests": { "cpu": "1", "memoryInGB": "1.5" } },
            }
          ],
          "ipAddress": { "type": "Public", "ports": [{ "port": "3000", "protocol": "TCP" }] },
          "osType": "Linux",
          "location": container_group_location,
          "restartPolicy": "OnFailure",
        };

        # send request and wait until done
        result = azure_client.container_groups.begin_create_or_update(
            resource_group_name,
            container_group_name,
            container_group
        ).result()

    # print ref
    print(f"Deployment for image {addr} now available at http://{result.ip_address.ip}:{result.ip_address.ports[0].port}")

anyio.run(main)
