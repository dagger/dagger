import sys

from dagger import Engine
from gql.dsl import DSLQuery, DSLSchema, dsl_gql
from graphql.language import print_ast


def main(msg: str):
    with Engine() as session:
        assert session.client.schema is not None
        ds = DSLSchema(session.client.schema)

        query = dsl_gql(DSLQuery(
            ds.Query.container.select(
                # `from` is a reserved keyword so use getattr
                getattr(ds.Container, "from")(address="python:alpine").select(
                    ds.Container.exec(args=["pip", "install", "cowsay"]).select(
                        ds.Container.exec(args=["cowsay", msg]).select(
                            ds.Container.stdout.select(
                                ds.File.contents
                            )
                        )
                    )
                )
            )
        ))

        result = session.execute(query)

        print(f"query = {print_ast(query)}")
        print(result['container']['from']['exec']['exec']['stdout']['contents'])


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "Hey there!")
