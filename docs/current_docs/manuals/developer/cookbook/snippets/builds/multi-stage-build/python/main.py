import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def build(self, src: dagger.Directory) -> str:
        """Build and publish Docker container"""
        # build app
        builder = (
            dag.container()
            .from_("golang:latest")
            .with_directory("/src", src)
            .with_workdir("/src")
            .with_env_variable("CGO_ENABLED", "0")
            .with_exec(["go", "build", "-o", "myapp"])
        )

        # publish binary on alpine base
        prod_image = (
            dag.container()
            .from_("alpine")
            .with_file("/bin/myapp", builder.file("/src/myapp"))
            .with_entrypoint(["/bin/myapp"])
        )

        # publish to ttl.sh registry
        addr = prod_image.publish("ttl.sh/myapp:latest")

        return addr
