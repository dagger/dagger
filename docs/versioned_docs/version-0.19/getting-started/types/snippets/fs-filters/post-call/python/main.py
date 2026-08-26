import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def foo(self, source: dagger.Directory) -> dagger.Container:
        builder = (
            dag.container()
            .from_("golang:latest")
            .with_directory("/src", source, exclude=["*.git", "internal"])
            .with_workdir("/src/hello")
            .with_exec(["go", "build", "-o", "hello.bin", "."])
        )

        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory(
                "/app", builder.directory("/src/hello"), include=["hello.bin"]
            )
            .with_entrypoint(["/app/hello.bin"])
        )
