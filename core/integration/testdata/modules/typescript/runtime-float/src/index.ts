import { dag, float, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  test(n: float): float {
    return n
  }

  @func()
  testFloat32(n: float): float {
    return n
  }

  @func()
  async dep(n: float): Promise<float> {
    return dag.dep().dep(n)
  }
}
