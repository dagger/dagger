import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def archiver(self, src: dagger.Directory) -> dagger.File:
        return (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["apk", "add", "zip"])
            .with_mounted_directory("/src", src)
            .with_workdir("/src")
            .with_exec(["sh", "-c", "zip -p -r out.zip *.*"])
            .file("/src/out.zip")
        )
