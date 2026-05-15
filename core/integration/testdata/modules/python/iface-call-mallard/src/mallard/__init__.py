import dagger


@dagger.object_type
class Mallard:
    @dagger.function
    def quack(self) -> str:
        return "mallard quack"
