
import { object, func } from "@dagger.io/dagger"

@object()
export class X {
  @func()
  message: string

  @func()
  timestamp: string

  @func()
  recipient: string

  @func()
  from: string

  constructor(message: string, timestamp: string, recipient: string, from: string) {
    this.message = message;
    this.timestamp = timestamp;
    this.recipient = recipient;
    this.from = from;
  }
}

@object()
export class Test {
  @func()
  myFunction(): X {
    return new X("foo", "now", "user", "admin");
  }
}
