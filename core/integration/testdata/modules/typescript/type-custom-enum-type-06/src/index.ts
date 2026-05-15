import { dag, func, object, DepStatus } from "@dagger.io/dagger"

@object()
export class Test {
  status: DepStatus

  constructor() {
    this.status = DepStatus.Active;
  }

  @func()
  active(): string {
    return this.status;
  }

  @func()
  async inactive(): Promise<string> {
    let status = await dag.dep().active();
    status = await dag.dep().invert(status);
    return status;
  }
}
