from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    def build(
        self, source: Annotated[dagger.Directory, Doc("Source code location")]
    ) -> dagger.Container:
        """Build an application using cached dependencies"""
        return (
            dag.container()
            .from_("node:21")
            .with_directory("/src", source)
            .with_workdir("/src")
            .with_mounted_cache("/root/.npm", dag.cache_volume("node-21"))
            .with_exec(["npm", "install"])
        )
