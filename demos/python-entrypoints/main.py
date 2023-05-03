import strawberry

import dagger
from dagger.server import Server

# TODO: probably not needed
from dagger.server.cli import app


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
                .with_exec(["apk", "add", "curl"])
                .with_exec(["curl", "https://dagger.io/"])
                .stdout()
            )


@strawberry.type(extend=True)
class Query:
    @strawberry.field
    def hello(self) -> Hello:
        return Hello()


schema = strawberry.Schema(query=Query)

server = Server(schema, debug=True)

# TODO: probably not needed
app(prog_name="dagger-server-py")
