import dagger


@dagger.object_type
class Dep:
    @dagger.function
    def hello(self) -> str:
        return "hello"
