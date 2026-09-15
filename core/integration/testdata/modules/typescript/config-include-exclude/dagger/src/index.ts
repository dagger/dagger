import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  fn(): Directory {
    return dag.currentModule().source()
  }
}
