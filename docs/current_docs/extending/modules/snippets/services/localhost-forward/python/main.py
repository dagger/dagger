import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def http_service(self) -> dagger.Service:
        """Start and return an HTTP service."""
        return (
            dag.container()
            .from_("python")
            .with_workdir("/srv")
            .with_new_file("index.html", "Hello, world!")
            .with_exposed_port(8080)
            .as_service(args=["python", "-m", "http.server", "8080"])
        )

    @function
    async def get(self) -> str:
        """Send a request to an HTTP service accessible on localhost."""
        return await (
            dag.container()
            .from_("alpine")
            .with_localhost_forward(8080, self.http_service())
            .with_exec(["wget", "-O-", "http://127.0.0.1:8080"])
            .stdout()
        )
