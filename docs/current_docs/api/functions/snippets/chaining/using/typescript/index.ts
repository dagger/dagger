import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  example(buildSrc: Directory, buildArgs: string[]): Directory {
    return dag.golang().build({ source: buildSrc, args: buildArgs }).terminal()
  }
}
