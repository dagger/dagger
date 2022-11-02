from textwrap import dedent

import pytest

import dagger

pytestmark = pytest.mark.anyio


@pytest.fixture
async def client():
    async with dagger.Connection() as client:
        yield client


async def test_query(client: dagger.Client):
    alpine = client.container().from_("python:3.10.8-alpine")

    version = await alpine.exec(["python", "-V"]).stdout().contents()
    assert version.value == "Python 3.10.8\n"

    assert version.query == dedent(
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
