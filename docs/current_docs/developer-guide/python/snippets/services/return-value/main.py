import dagger
from dagger import dag, function, object_type

@object_type
class MyModule:
    @function
    def http_service(self) -> dagger.Service:
        """Starts and returns an HTTP service"""
        return (
            dag.container()
            .from_("python")
            .with_directory("/srv", dag.directory().with_new_file("index.html", "Hello, world!"))
            .with_workdir("/srv")
            .with_exec(["python", "-m", "http.server", "8080"])
            .with_exposed_port(8080)
            .as_service()
        )

    @function
    async def get(self) -> str:
        """Sends a request to an HTTP service and returns the response"""
        return await (
            dag.container()
            .from_("alpine")
            .with_service_binding("www", self.http_service())
            .with_exec(["wget", "-O-", "http://www:8080"])
            .stdout()
        )
