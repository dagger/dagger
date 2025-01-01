import dagger

from dagger import dag, function, object_type
from opentelemetry import trace


@object_type
class MyModule:
    @function
    async def foo(self) -> dagger.Directory:
        tracer = trace.get_tracer(__name__)

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
            # create a span for each file creation operation
            with tracer.start_as_current_span(
                "create-file", attributes={"file.name": name}
            ):
                # create the file and add it to the container
                container = container.with_new_file(name, content)

        return container.directory("/results")
