import sys
import anyio
import dagger
import uuid


async def pipeline():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        project = client.git("https://github.com/dagger/dagger").branch("main").tree()

        build = (
            client.container()
            .from_("golang:1.20")
            .with_directory("/src", project)
            .with_workdir("/src")
            .with_exec(["go", "build", "./cmd/dagger"])
        )

        prod_image = (
            client.container()
            .from_("cgr.dev/chainguard/wolfi-base:latest")
            .with_file("/bin/dagger", build.file("/src/dagger"))
            .with_entrypoint(["/bin/dagger"])
        )

        id = str(uuid.uuid4())
        tag = f'ttl.sh/dagger-{id}:1h'

        await prod_image.publish(tag)




if __name__ == "__main__":
    anyio.run(pipeline)
