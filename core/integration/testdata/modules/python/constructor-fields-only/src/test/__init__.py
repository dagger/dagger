from dagger import dag, field, object_type


@object_type
class Test:
    alpine_version: str = field()

    @classmethod
    async def create(cls) -> "Test":
        return cls(
            alpine_version=await (
                dag.container()
                .from_("alpine:3.22.1")
                .file("/etc/alpine-release")
                .contents()
            )
        )
