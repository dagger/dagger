import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def load(self, docker: dagger.Socket, tag: str) -> dagger.Container:

        # create a new container
        ctr = dag.container().from_("alpine").with_exec(["apk", "add", "git"])

        # create a new container from the docker CLI image
        # mount the Docker socket from the host
        # mount the newly-built container as a tarball
        docker_cli = (
            dag.container()
            .from_("docker:cli")
            .with_unix_socket("/var/run/docker.sock", docker)
            .with_mounted_file("image.tar", ctr.as_tarball())
        )

        # load the image from the tarball
        out = await docker_cli.with_exec(["docker", "load", "-i", "image.tar"]).stdout()

        # add the tag to the image
        image = out.strip().split(":", 1)[1].strip()
        return docker_cli.with_exec(["docker", "tag", image, tag])
