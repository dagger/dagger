import { dag, object, func } from '@dagger.io/dagger'

@object()
export class Test {
  @func()
  async useHello(): Promise<string> {
    return dag.dep().hello()
  }
}
