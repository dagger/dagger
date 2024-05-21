import dagger
from dagger import function, object_type


@object_type
class HelloDagger:
    @function
    async def test(self, source: dagger.Directory) -> str:
        """Return the result of running unit tests"""
        return (
            await (
                # get the build environment container
                # by calling another Dagger Function
                self.build_env(source)
                # call the test runner
                .with_exec(["npm", "run", "test:unit", "run"])
                # capture and return the command output
                .stdout()
            )
        )
