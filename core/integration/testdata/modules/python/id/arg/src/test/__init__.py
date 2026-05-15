import dagger


@dagger.object_type
class Test:
    @dagger.function
    def fn(self, id_: str) -> str:
        return id_
