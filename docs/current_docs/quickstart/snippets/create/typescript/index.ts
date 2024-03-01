import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class Example {
  @func()
  async buildAndPublish(
    buildSrc: Directory,
    buildArgs: string[],
  ): Promise<string> {
    const ctr = dag.wolfi().container()

    return await dag
      .golang()
      .buildContainer({ source: buildSrc, args: buildArgs, base: ctr })
      .publish("ttl.sh/my-api-server-" + Math.floor(Math.random() * 10000000))
  }
}
