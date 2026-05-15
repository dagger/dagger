import dagger

@dagger.object_type
class Message:
    content: str = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def say_hello(self, name: str) -> Message:
        return Message(content=f"hello {name}")

    @dagger.function
    def upper(self, msg: Message) -> Message:
        msg.content = msg.content.upper()
        return msg

    @dagger.function
    def uppers(self, msg: list[Message]) -> list[Message]:
        for m in msg:
            m.content = m.content.upper()
        return msg
