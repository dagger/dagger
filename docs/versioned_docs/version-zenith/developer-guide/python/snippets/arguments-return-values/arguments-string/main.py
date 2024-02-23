import dagger
from dagger import dag, object_type, function

@object_type
class MyModule:

    @function
    async def get_user(gender: str) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["apk", "add", "curl"])
            .with_exec(["apk", "add", "jq"])
            .with_exec(["sh", "-c", f"curl https://randomuser.me/api/?gender={gender} | jq .results[0].name"])
            .stdout()
        )
