
import { object, func } from "@dagger.io/dagger"

@object()
export class Repeater {
  @func()
  message: string

  @func()
  times: number

  constructor(message: string, times: number) {
    this.message = message
    this.times = times
  }

  @func()
  render(): string {
    return this.message.repeat(this.times)
  }
}

@object()
export class Test {
  @func()
  repeater(msg: string, times: number): Repeater {
    return new Repeater(msg, times)
  }
}
