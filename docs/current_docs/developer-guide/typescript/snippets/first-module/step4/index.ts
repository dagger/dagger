import { object, func, field } from "@dagger.io/dagger"

@object()
class Potato {
  /**
   * @param count The number of potatoes to process
   * @param mashed Whether the potatoes are mashed
   */
  @func()
  helloWorld(count: number, mashed = false): PotatoMessage {
    let m: string
    if (mashed) {
      m = `Hello Daggernauts, I have mashed ${count} potatoes`
    } else {
      m = `Hello Daggernauts, I have ${count} potatoes`
    }
    return new PotatoMessage(m, "potato@example.com")
  }
}

@object()
class PotatoMessage {
  @field()
  message: string

  @field()
  from: string

  constructor(message: string, from: string) {
    this.message = message
    this.from = from
  }
}
