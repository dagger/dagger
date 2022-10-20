import dagger
import strawberry
from gql import gql


@strawberry.type
class Hello:
    greeting: str = "Hello"

    @strawberry.field
    def say(self, msg: str) -> str:
        return f"{self.greeting} {msg}!"

    @strawberry.field
    async def html(self) -> str:
        # embedded gql request to the core api
        async with dagger.Client() as session:
            result = await session.execute(
                gql(
                    """{
                        container {
                            from(address: "alpine") {
                                exec(args: ["apk", "add", "curl"]) {
                                    exec(args: ["curl", "https://dagger.io/"]) {
                                        stdout {
                                            contents
                                        }
                                    }
                                }
                            }
                        }
                    }"""
            )
        )
        return result["container"]["from"]["exec"]["exec"]["stdout"]["contents"]


@strawberry.type(extend=True)
class Query:
    @strawberry.field
    def hello(self) -> Hello:
        return Hello()


schema = strawberry.Schema(query=Query)

server = dagger.Server(schema, debug=True)
