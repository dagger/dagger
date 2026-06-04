import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def read_file_http(self, url: str) -> dagger.Container:
        file = dag.http(url)
        return dag.container().from_("alpine:latest").with_file("/src/myfile", file)
