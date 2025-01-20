import { object, func } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  addInteger(a: number, b: number): number {
    return a + b
  }
}
