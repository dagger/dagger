import { func, object, field } from "../../../decorators/decorators.js"

@object()
class Message {
  @field()
  content: string

  constructor(content: string) {
    this.content = content
  }

  toUpperCase() {
    return this.content.toUpperCase()
  }
}

@object()
class ObjectParam {
  @func()
  sayHello(name: string): Message {
    return new Message("hello " + name)
  }

  @func()
  upper(msg: Message): Message {
    msg.content = msg.toUpperCase()
    return msg
  }

  @func()
  uppers(msg: Message[]): Message[] {
    for (let i = 0; i < msg.length; i++) {
      msg[i].content = msg[i].content.toUpperCase()
    }
    return msg
  }
}
