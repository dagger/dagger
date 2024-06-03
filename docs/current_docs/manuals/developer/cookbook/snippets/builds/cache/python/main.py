from datetime import datetime

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def build(self) -> str:
        """Run a build with cache invalidation"""
        output = (
            dag.container()
            .from_("alpine")
            # comment out the line below to see the cached date output
            .with_env_variable("CACHEBUSTER", str(datetime.now()))
            .with_exec(["date"])
            .stdout()
        )
        return await output