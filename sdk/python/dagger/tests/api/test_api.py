from textwrap import dedent

import pytest
from graphql import print_ast

import dagger
from dagger.client.api.gen import Query


@pytest.fixture
def api():
    with dagger.Client() as session:
        yield Query.from_session(session)


def test_query(api: Query, mocker):
    spy = mocker.spy(api._ctx.session, "execute")

    alpine = api.container().from_(address="python:3.10.8-alpine")
    version = alpine.exec(args=["python", "-V"]).stdout().contents()

    assert version == "Python 3.10.8\n"

    query = spy.call_args.args[0]

    assert print_ast(query) == dedent(
        """\
        {
          container {
            from(address: "python:3.10.8-alpine") {
              exec(args: ["python", "-V"]) {
                stdout {
                  contents
                }
              }
            }
          }
        }
        """.rstrip()
    )
