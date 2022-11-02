from .base import Result, Root

# FIXME: Codegen fails if this fails. Didn't put in try...catch because it messes with the IDE's type hints.
from .gen import Query


# FIXME: Make this autodiscoverable. Users will need to generate when extensions are enabled.
class Client(Query, Root):
    """Client for the Dagger API.

    The API is chainable and reusable until leaf values, which can be awaited immediately.

    Example::

        async with dagger.Connection() as client:
            alpine = client.container().from_("python:3.10.8-alpine")
            version = await alpine.exec(["python", "-V"]).stdout().contents()
            assert version.value == "Python 3.10.8\n"

    """

    @property
    def graphql_name(self) -> str:
        return "Query"


__all__ = [
    "Client",
    "Result",
]
