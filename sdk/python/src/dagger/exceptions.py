import attrs
import graphql


class DaggerError(Exception):
    """Base exception for all Dagger exceptions."""


class ProvisionError(DaggerError):
    """Error while provisioning the Dagger engine."""


class SessionError(ProvisionError):
    """Error while starting an engine session."""

    def __str__(self) -> str:
        return f"Dagger engine failed to start: {super().__str__()}"


class ClientError(DaggerError):
    """Base class for client errors."""


class InvalidQueryError(ClientError):
    """Misuse of the query builder."""


@attrs.define
class QueryErrorLocation:
    line: int
    column: int


class QueryError(ClientError):
    """The server returned an error for a specific query."""

    def __init__(
        self,
        msg: str,
        query: graphql.DocumentNode,
        path: list[str],
        locations: list[QueryErrorLocation],
    ):
        super().__init__(msg.strip())
        self.query = query
        self.path = path
        self.locations = locations

    def debug_query(self):
        """Return GraphQL query for debugging purposes."""
        lines = graphql.print_ast(self.query).splitlines()
        # count number of digits from line count
        pad = len(str(len(lines)))
        locations = {loc.line: loc.column for loc in self.locations}
        res = []
        for nr, line in enumerate(lines, start=1):
            # prepend line number
            res.append(f"{{:{pad}d}}: {{}}".format(nr, line))
            if nr in locations:
                # add caret below line, pointing to start of error
                res.append(" " * (pad + 1 + locations[nr]) + "^")
        return "\n".join(res)


class ExecuteTimeoutError(ClientError):
    """Timeout while executing a query."""
