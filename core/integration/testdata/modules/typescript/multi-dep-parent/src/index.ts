import { dag, object, func } from '@dagger.io/dagger'

@object()
export class Test {
  @func()
  async names(): Promise<string[]> {
    return [await dag.foo().name(), await dag.bar().name()]
  }
}
