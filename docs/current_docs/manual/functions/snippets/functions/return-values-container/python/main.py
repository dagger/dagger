import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def alpine_builder(self, packages: list[str]) -> dagger.Container:
        ctr = dag.container().from_("alpine:latest")
        for pkg in packages:
            ctr = ctr.with_exec(["apk", "add", pkg])
        return ctr
