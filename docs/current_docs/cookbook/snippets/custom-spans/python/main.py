import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def foo(self) -> dagger.Directory:
        # define the files to be created and their contents
        files = {
            "file1.txt": "foo",
            "file2.txt": "bar",
            "file3.txt": "baz",
        }

        # set up an alpine container with the directory mounted
        container = (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/results", dag.directory())
            .with_workdir("/results")
        )

        for name, content in files.items():
            # create files
            container = container.with_new_file(name, content)
            # emit custom spans for each file created
            log = f"Created file: {name} with contents: {content}"
            print(log)

        return container.directory("/results")
