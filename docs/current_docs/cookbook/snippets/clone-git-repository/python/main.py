import dagger
from dagger import dag, enum_type, function, object_type


@enum_type
class Locator(dagger.Enum):
    BRANCH = "BRANCH"
    TAG = "TAG"
    COMMIT = "COMMIT"


@object_type
class MyModule:
    @function
    async def clone(
        self, repository: str, locator: Locator, ref: str
    ) -> dagger.Container:
        r = dag.git(repository)

        if locator == Locator.BRANCH:
            d = r.branch(ref).tree()
        elif locator == Locator.TAG:
            d = r.tag(ref).tree()
        elif locator == Locator.COMMIT:
            d = r.commit(ref).tree()
        else:
            raise ValueError

        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", d)
            .with_workdir("/src")
        )
