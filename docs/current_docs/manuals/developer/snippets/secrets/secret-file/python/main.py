import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def github_api(self, file: dagger.Secret) -> str:
        return await (
            dag.container()
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "github-cli"])
            .with_mounted_secret("/root/.config/gh/hosts.yml", file)
            .with_workdir("/root")
            .with_exec(["gh", "auth", "status"])
            .stdout()
        )
