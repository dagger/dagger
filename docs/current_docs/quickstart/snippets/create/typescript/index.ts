import { dag, Directory, object, func } from "@dagger.io/dagger"

@object
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class MyModule {
  @func
  async buildAndPublish(
    buildSrc: Directory,
    buildArgs: string[],
    outFile: string,
  ): Promise<string> {
    // build project and return binary file
    const file = dag
      .golang()
      .withProject(buildSrc)
      .build(buildArgs)
      .file(outFile)

    // build and publish container with binary file
    return dag
      .wolfi()
      .base()
      .container()
      .withFile("/usr/local/bin/dagger", file)
      .publish(
        "ttl.sh/my-dagger-container-" + Math.floor(Math.random() * 10000000),
      )
  }
}
