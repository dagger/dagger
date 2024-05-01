import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def github_api(self, endpoint: str, token: dagger.Secret) -> str:
        return await (
            dag.container()
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "curl"])
            .with_secret_variable("GITHUB_TOKEN", token)
            .with_exec(
                [
                    "sh",
                    "-c",
                    f"""curl "{endpoint}" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $GITHUB_TOKEN" """,
                ]
            )
            .stdout()
        )
