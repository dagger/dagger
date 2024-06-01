from dagger import DaggerError, dag, function, object_type


@object_type
class MyModule:
    @function
    async def test(self) -> str:
        """Generate an error"""
        try:
            return (
                await (
                    dag.container()
                    .from_("alpine")
                    # ERROR: cat: read error: Is a directory
                    .with_exec(["cat", "/"])
                    .stdout()
                )
            )
        except DaggerError as e:
            # DaggerError is the base class for all errors raised by dagger.
            return "Test pipeline failure: " + e.stderr
