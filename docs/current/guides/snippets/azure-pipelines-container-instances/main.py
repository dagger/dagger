import os
import sys
from enum import StrEnum, auto

import anyio
from azure.identity.aio import DefaultAzureCredential
from azure.mgmt.containerinstance.aio import ContainerInstanceManagementClient

import dagger

# configure container group, name and location
CONTAINER_NAME = "my-app"
CONTAINER_GROUP_NAME = "my-app"
CONTAINER_GROUP_LOCATION = "eastus"
RESOURCE_GROUP_NAME = "my-group"


class Env(StrEnum):
    """Required environment variables."""

    def _generate_next_value_(name, *_) -> str:
        if name not in os.environ:
            msg = f"Environment variable must be set: {name}"
            raise OSError(msg)
        return os.environ[name]

    DOCKERHUB_USERNAME = auto()
    DOCKERHUB_PASSWORD = auto()
    AZURE_SUBSCRIPTION_ID = auto()
    AZURE_TENANT_ID = auto()
    AZURE_CLIENT_ID = auto()
    AZURE_CLIENT_SECRET = auto()

    def as_secret(self, client: dagger.Client) -> dagger.Secret:
        return client.set_secret(self.name.lower(), self.value)


async def main():
    # initialize Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as dagger_client:
        # get reference to the project directory
        source = dagger_client.host().directory(".", exclude=["node_modules", "ci"])

        # get Node image
        node = dagger_client.container(platform=dagger.Platform("linux/amd64")).from_(
            "node:18"
        )

        # mount source code directory into Node image
        # install dependencies
        # set entrypoint
        ctr = (
            node.with_directory("/src", source)
            .with_workdir("/src")
            .with_exec(["cp", "-R", ".", "/home/node"])
            .with_workdir("/home/node")
            .with_exec(["npm", "install"])
            .with_entrypoint(["npm", "start"])
        )

        # publish image
        addr = await ctr.with_registry_auth(
            "docker.io",
            Env.DOCKERHUB_USERNAME,
            Env.DOCKERHUB_PASSWORD.as_secret(dagger_client),
        ).publish(f"{Env.DOCKERHUB_USERNAME}/my-app")

    print(f"Published at: {addr}")

    # define deployment request
    container_group = {
        "containers": [
            {
                "name": CONTAINER_NAME,
                "image": addr,
                "ports": [{"port": "3000"}],
                "resources": {"requests": {"cpu": "1", "memoryInGB": "1.5"}},
            }
        ],
        "ipAddress": {
            "type": "Public",
            "ports": [{"port": "3000", "protocol": "TCP"}],
        },
        "osType": "Linux",
        "location": CONTAINER_GROUP_LOCATION,
        "restartPolicy": "OnFailure",
    }

    # initialize Azure client
    async with DefaultAzureCredential() as credential:
        async with ContainerInstanceManagementClient(
            credential=credential,
            subscription_id=Env.AZURE_SUBSCRIPTION_ID,
        ) as azure_client:
            # send request and wait until done
            response = await azure_client.container_groups.begin_create_or_update(
                RESOURCE_GROUP_NAME,
                CONTAINER_GROUP_NAME,
                container_group,
            )
            result = await response.result()

    print(
        f"Deployment for image {addr} now available at "
        f"http://{result.ip_address.ip}:{result.ip_address.ports[0].port}"
    )


anyio.run(main)
