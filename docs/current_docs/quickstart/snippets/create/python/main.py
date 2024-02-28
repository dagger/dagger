import uuid

import dagger
from dagger import dag, function, object_type


@object_type
class TestModule:
    @function
    async def build_and_publish(
        self,
        build_src: dagger.Directory,
        build_args: list[str],
        out_file: str,
    ) -> str:
        # build project and return binary file
        file = dag.golang().with_project(build_src).build(build_args).file(out_file)

        # build and publish container with binary file
        return await (
            dag.wolfi()
            .container()
            .with_file("/usr/local/bin/dagger", file)
            .publish(f"ttl.sh/my-dagger-container-{uuid.uuid4().hex[:8]}:10m")
        )
