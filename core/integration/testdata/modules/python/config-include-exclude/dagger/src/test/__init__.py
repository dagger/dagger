import dagger
from dagger import dag, function, object_type


@object_type
class Test:
    @function
    def fn(self) -> dagger.Directory:
        return dag.current_module().source()
