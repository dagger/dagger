import { dag, object, func } from "@dagger.io/dagger"

export interface Duck {
  quack: () => Promise<string>
}

@object()
export class Test {
  @func()
  getDuck(): Duck {
    return dag.mallard()
  }
}
