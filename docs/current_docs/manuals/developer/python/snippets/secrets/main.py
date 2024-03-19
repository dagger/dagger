import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def github_api(self, endpoint: str, token: dagger.Secret) -> str:
        plaintext = await token.plaintext()
        return await (
            dag.container()
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "curl"])
            .with_exec(
                [
                    "sh",
                    "-c",
                    f"""curl "{endpoint}" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer {plaintext}" """,
                ]
            )
            .stdout()
        )
