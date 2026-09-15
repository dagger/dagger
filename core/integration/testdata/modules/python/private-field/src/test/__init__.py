import dagger


@dagger.object_type
class Test:
    foo: str = dagger.field(default="")
    bar: str = ""

    @dagger.function
    def set(self, foo: str, bar: str) -> "Test":
        self.foo = foo
        self.bar = bar
        return self

    @dagger.function
    def hello(self) -> str:
        return self.foo + self.bar
