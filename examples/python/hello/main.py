import dagger
import strawberry


@strawberry.type
class Hello:

    lang: str = "test"

    @strawberry.field
    def say(self, msg: str) -> str:
        return 'Hello {}!'.format(msg)


@strawberry.type
class Query:
    hello: Hello


if __name__ == '__main__':
    s = dagger.Server()
    s.run()
