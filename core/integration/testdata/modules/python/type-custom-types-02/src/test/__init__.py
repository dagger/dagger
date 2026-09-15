import dagger

@dagger.object_type
class Repeater:
    message: str = dagger.field(default="")
    times: int = dagger.field(default=0)

    @dagger.function
    def render(self) -> str:
        return self.message * self.times


@dagger.object_type
class Test:
    @dagger.function
    def repeater(self, msg: str, times: int) -> Repeater:
        return Repeater(message=msg, times=times)
