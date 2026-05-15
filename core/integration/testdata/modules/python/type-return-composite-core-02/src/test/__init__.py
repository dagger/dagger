import dagger
from dagger import dag

@dagger.object_type
class Foo:
    con: dagger.Container = dagger.field()
    unset_file: dagger.File | None = dagger.field(default=None)

@dagger.object_type
class Test:
    @dagger.function
    def my_slice(self) -> list[dagger.Container]:
        return [dag.container().from_("alpine:3.22.1").with_exec(["echo", "hello world"])]

    @dagger.function
    def my_struct(self) -> Foo:
        return Foo(con=dag.container().from_("alpine:3.22.1").with_exec(["echo", "hello world"]))
