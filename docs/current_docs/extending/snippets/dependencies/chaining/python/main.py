import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def example(
        self, build_src: dagger.Directory, build_args: list[str]
    ) -> dagger.Directory:
        return dag.golang().build(source=build_src, args=build_args).terminal()
