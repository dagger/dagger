
import { object, func } from "@dagger.io/dagger"

@object()
export class X {
  @func()
  message: string

  constructor(message: string) {
    this.message = message;
  }
}

@object()
export class Test {
  @func()
  myFunction(): X {
    return new X("foo");
  }
}
