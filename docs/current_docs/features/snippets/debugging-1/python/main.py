<<<<<<< HEAD
<<<<<<< HEAD
=======
import dagger
>>>>>>> 81388975a (Added code snippets to feature pages)
=======
>>>>>>> 2f22413a8 (Fix linter)
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def foo(self) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["sh", "-c", "echo hello world > /foo"])
            .with_exec(["cat", "/FOO"])  # deliberate error
            .stdout()
        )


# run with dagger call --interactive foo
