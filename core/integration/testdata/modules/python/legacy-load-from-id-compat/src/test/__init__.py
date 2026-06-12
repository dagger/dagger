import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    async def round_trip(self) -> str:
        id_ = await dag.container().from_("alpine:3.22.1").id()
        return await (
            dag.load_container_from_id(dagger.ContainerID(id_))
            .with_exec(["echo", "ok"])
            .stdout()
        )
