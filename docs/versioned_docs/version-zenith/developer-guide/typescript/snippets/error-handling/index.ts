import { object, func } from "@dagger.io/dagger"

@object()
class HelloWorld {
  @func()
  divide(a: number, b: number): number {
    if (b <= 0) {
      throw new Error("cannot divide by zero")
    }

    return a / b
  }
}
