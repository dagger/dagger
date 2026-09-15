import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    def container_echo(self, string_arg: str) -> dagger.Container:
        return dag.container().from_("alpine:3.22.1").with_exec(["echo", string_arg])
