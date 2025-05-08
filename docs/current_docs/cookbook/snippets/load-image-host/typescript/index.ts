import { dag, object, func, Container, Socket } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async load(docker: Socket, tag: string): Promise<Container> {
    // create a new container
    const ctr = dag.container().from("alpine")
      .withExec(["apk", "add", "git"])

    // create a new container from the docker CLI image
    // mount the Docker socket from the host
    // mount the newly-built container as a tarball
    const dockerCli = dag
      .container()
      .from("docker:cli")
      .withUnixSocket("/var/run/docker.sock", docker)
      .withMountedFile("image.tar", ctr.asTarball())

    // load the image from the tarball
    const out = await dockerCli
      .withExec(["docker", "load", "-i", "image.tar"])
      .stdout()

    // add the tag to the image
    const image = out.split(/:(.+)/)[1].trim()
    return await dockerCli.withExec(["docker", "tag", image, tag]).sync()
  }
}
