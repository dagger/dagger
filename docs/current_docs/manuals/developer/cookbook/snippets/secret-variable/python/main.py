import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def github_api(self, token: dagger.Secret) -> str:
        return await (
            dag.container(platform=dagger.Platform("linux/amd64"))
            .from_("alpine:3.17")
            .with_secret_variable("GITHUB_API_TOKEN", token)
            .with_exec(["apk", "add", "curl"])
            .with_exec(
                [
                    "sh",
                    "-c",
                    """curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_API_TOKEN" """,
                ]
            )
            .stdout()
        )
