import strawberry
from gql import gql

import dagger


@strawberry.type
class Hello:
    greeting: str = "Hello"

    @strawberry.field
    def say(self, msg: str) -> str:
        return f"{self.greeting} {msg}!"

    @strawberry.field
    def html(self, lines: int | None = 1) -> str:
        # embedded gql request to the core api
        c = dagger.Client()
        result = c.execute(
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
