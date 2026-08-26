import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def agent(self) -> dagger.File:
        dirname = dag.git("github.com/golang/example").branch("master").tree()
        builder = dag.container().from_("golang:latest")

        environment = (
            dag.env()
            .with_container_input("container", builder, "a Golang container")
            .with_directory_input("directory", dirname, "a directory with source code")
            .with_file_output("file", "the built Go executable")
        )

        work = (
            dag.llm()
            .with_env(environment)
            .with_prompt(
                """
                You have access to a Golang container.
                You also have access to a directory containing Go source code.
                Mount the directory into the container and build the Go application.
                Once complete, return only the built binary.
                """
            )
        )

        return work.env().output("file").as_file()
