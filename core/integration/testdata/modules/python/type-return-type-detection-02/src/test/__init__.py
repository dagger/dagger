import dagger

@dagger.object_type
class X:
    message: str = dagger.field(default="")

@dagger.object_type
class Test:
    @dagger.function
    def my_function(self) -> X:
        return X(message="foo")
