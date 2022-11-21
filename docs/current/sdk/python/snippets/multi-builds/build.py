"""
Create a multi-build pipeline for a Go application.
"""


import itertools
import sys

import anyio
import graphql

import dagger


async def build():
    print("Building with Dagger")

    # define build matrix
    oses = ["linux", "darwin"]
    arches = ["amd64", "arm64"]

    # initialize dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # get reference to the local project
        src = client.host().directory(".")

        # create empty directory to put build outputs
        outputs = client.directory()

        golang = (
            # get `golang` image
            client.container()
            .from_("golang:latest")
            # mount source code into `golang` image
            .with_mounted_directory("/src", src)
            .with_workdir("/src")
        )

        for goos, goarch in itertools.product(oses, arches):
            # create a directory for each OS and architecture
            path = f"build/{goos}/{goarch}/"

            build = (
                golang
                # set GOARCH and GOOS in the build environment
                .with_env_variable("GOOS", goos)
                .with_env_variable("GOARCH", goarch)
                .with_exec(["go", "build", "-o", path])
            )

            # add build to outputs
            outputs = outputs.with_directory(path, build.directory(path))

        # write build artifacts to host
        await outputs.export(".")


if __name__ == "__main__":
    try:
        anyio.run(build)
    except graphql.GraphQLError as e:
        print(e.message, file=sys.stderr)
        sys.exit(1)
