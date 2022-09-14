import dagger
import strawberry
from gql import gql


@strawberry.type
class Hello:

    greeting: str = "Hello"

    @strawberry.field
    def say(self, msg: str) -> str:
        return '{} {}!'.format(self.greeting, msg)

    @strawberry.field
    def html(self, lines: int = 1) -> str:
        # embedded gql request to the core api
        c = dagger.Client()
        result = c.execute(
            gql("""query ($lines: Int) {
                    core {
                        image(ref: "alpine") {
                            exec(input: {args: ["apk", "add", "curl"]}) {
                                fs {
                                    exec(input: {args: ["curl", "https://dagger.io/"]}) {
                                        stdout(lines: $lines)
                                    }
                                }
                            }
                        }
                    }
                }"""),
            variable_values={'lines': lines})
        return result['core']['image']['exec']['fs']['exec']['stdout']


@strawberry.type
class Query:
    hello: Hello


if __name__ == '__main__':
    s = dagger.Server()
    s.run()
