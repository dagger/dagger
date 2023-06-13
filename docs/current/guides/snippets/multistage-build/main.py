import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get host directory
        project = client.host().directory(".")

        # build app
        builder = (
            client.container()
            .from_("golang:latest")
            .with_directory("/src", project)
            .with_workdir("/src")
            .with_env_variable("CGO_ENABLED", "0")
            .with_exec(["go", "build", "-o", "myapp"])
        )

        # publish binary on alpine base
        prod = (
            client.container()
            .from_("alpine")
            .with_file("/bin/myapp", builder.file("/src/myapp"))
            .with_entrypoint(["/bin/myapp"])
        )
        addr = await prod.publish("localhost:5000/multistage")

    print(addr)


anyio.run(main)
