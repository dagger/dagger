import dagger


@dagger.object_type
class Example:
    @dagger.function
    def foo(self, bar: int) -> str:
        return f"number is: {bar}"
