from dagger import dag, function, object_type


@object_type
class MyModule:

    @function
    async def get_user(self) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["apk", "add", "curl"])
            .with_exec(["apk", "add", "jq"])
            .with_exec(["sh", "-c", "curl https://randomuser.me/api/ | jq .results[0].name"])
            .stdout()
        )
