
import { object, func } from "@dagger.io/dagger"

@object()
export class Bar {
  @func()
  msg: string;

  constructor(msg: string) {
    this.msg = msg;
  }
}

@object()
export class Foo {
  @func()
  msgContainer: Bar;

  constructor(msgContainer: Bar) {
    this.msgContainer = msgContainer;
  }
}

@object()
export class Test {
  @func()
  myFunction(): Foo {
    return new Foo(new Bar("hello world"));
  }
}
