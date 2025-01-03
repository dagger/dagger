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
        self, repository: str, locator: Locator, id: str
    ) -> dagger.Container:
        r = dag.git(repository)

        if locator == Locator.BRANCH:
            dir = r.branch(id).tree()
        elif locator == Locator.TAG:
            dir = r.tag(id).tree()
        elif locator == Locator.COMMIT:
            dir = r.commit(id).tree()
        else:
            raise ValueError("Invalid locator")

        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", dir)
            .with_workdir("/src")
        )
