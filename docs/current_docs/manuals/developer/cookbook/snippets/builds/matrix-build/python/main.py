import dagger
from dagger import dag, function, object_type

@object_type
class MyModule:
    @function
    async def build(self, src: dagger.Directory) -> dagger.Directory:
        """Build and return directory of go binaries"""
        # define build matrix
        gooses = ["linux", "darwin"]
        goarches = ["amd64", "arm64"]

        # create empty directory to put build artifacts
        outputs = dag.directory()

        golang = (
            dag.container()
            .from_("golang:latest")
            .with_directory("/src", src)
            .with_workdir("/src")
        )

        for goos in gooses:
            for goarch in goarches:
                # create directory for each OS and architecture
                path = f"build/{goos}/{goarch}/"

                # build artifact
                build = (
                    golang
                    .with_env_variable("GOOS", goos)
                    .with_env_variable("GOARCH", goarch)
                    .with_exec(["go", "build", "-o", path])
                )

                # add build to outputs
                outputs = outputs.with_directory(path, build.directory(path))

        return await outputs
