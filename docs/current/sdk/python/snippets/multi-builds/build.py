"""
Create a multi-build pipeline for a Go application.
"""

import sys

import anyio
import dagger
import graphql


async def build():
    print("Building with Dagger")

    # define build matrix
    oses = ["linux", "darwin"]
    arches = ["amd64", "arm64"]

    # initialize dagger client
    async with dagger.Connection() as client:

        # get reference to the local project
        src_id = await client.host().workdir().id()

        # create empty directory to put build outputs
        outputs = client.directory()

        golang = (
            # get `golang` image
            client.container().from_(f"golang:latest")

            # mount source code into `golang` image
            .with_mounted_directory("/src", src_id)
            .with_workdir("/src")
        )

        for goos in oses:
            for goarch in arches:

                # create a directory for each OS and architecture
                path = f"build/{goos}/{goarch}/"

                build = (
                    golang
                    # set GOARCH and GOOS in the build environment
                    .with_env_variable("GOOS", goos)
                    .with_env_variable("GOARCH", goarch)

                    # build application
                    .exec(["go", "build", "-o", path])
                )

                # get reference to build output directory in container
                dir_id = await build.directory(path).id()
                outputs = outputs.with_directory(path, dir_id)

        # write build artifacts to host
        await outputs.export(".")


if __name__ == "__main__":
    try:
        anyio.run(build)
    except graphql.GraphQLError as e:
        print(e.message)
        sys.exit(1)
