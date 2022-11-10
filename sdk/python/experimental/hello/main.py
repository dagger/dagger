import strawberry

import dagger


@strawberry.type
class Hello:
    greeting: str = "Hello"

    @strawberry.field
    def say(self, msg: str) -> str:
        return f"{self.greeting} {msg}!"

    @strawberry.field
    async def html(self) -> str:
        async with dagger.Connection() as client:
            return await (
                client.container()
                .from_("alpine")
                .exec(["apk", "add", "curl"])
                .exec(["curl", "https://dagger.io/"])
                .stdout()
                .contents()
            )


@strawberry.type(extend=True)
class Query:
    @strawberry.field
    def hello(self) -> Hello:
        return Hello()


schema = strawberry.Schema(query=Query)

server = dagger.Server(schema, debug=True)
