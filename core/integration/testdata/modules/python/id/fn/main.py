import dagger


@dagger.object_type
class Test:
    @dagger.function
    def id_(self) -> str:
        return "NOOOO!!!!"
