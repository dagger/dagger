import { func, object } from "../../../decorators/decorators.js"

@object()
class Message {
  @func()
  content: string

  constructor(content: string) {
    this.content = content
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
    msg.content = msg.content.toUpperCase()
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
