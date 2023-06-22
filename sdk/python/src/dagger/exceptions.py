from dataclasses import dataclass, field
from typing import Any

import cattrs
import graphql
from gql.transport.exceptions import TransportQueryError


class DaggerError(Exception):
    """Base exception for all Dagger exceptions."""


class ProvisionError(DaggerError):
    """Error while provisioning the Dagger engine."""


class DownloadError(ProvisionError):
    """Error while downloading the Dagger CLI."""

    def __str__(self) -> str:
        return f"Failed to download the Dagger CLI: {super().__str__()}"


class SessionError(ProvisionError):
    """Error while starting an engine session."""

    def __str__(self) -> str:
        return f"Failed to start Dagger engine session: {super().__str__()}"


class ClientError(DaggerError):
    """Base class for client errors."""


class ClientConnectionError(ClientError):
    """Error while establishing a client connection to the server."""

    def __str__(self) -> str:
        return (
            "Failed to establish client connection to the Dagger session: "
            f"{super().__str__()}"
        )


class TransportError(ClientError):
    """Error processing request/response during query execution."""


class ExecuteTimeoutError(TransportError):
    """Timeout while executing a query."""


class InvalidQueryError(ClientError):
    """Misuse of the query builder."""


@dataclass
class QueryErrorLocation:
    """Error location returned by the API."""

    line: int
    column: int


@dataclass
class QueryErrorValue:
    """Error value returned by the API."""

    message: str
    locations: list[QueryErrorLocation]
    path: list[str]
    extensions: dict[str, Any] = field(default_factory=dict)

    def __str__(self) -> str:
        return self.message


class QueryError(ClientError):
    """The server returned an error for a specific query."""

    _type = None

    def __new__(cls, errors: list[QueryErrorValue], *_):
        error_types = {
            subclass._type: subclass  # noqa: SLF001
            for subclass in cls.__subclasses__()
            if subclass._type  # noqa: SLF001
        }
        try:
            new_type = error_types[errors[0].extensions["_type"]]
        except (KeyError, IndexError):
            return super().__new__(cls)
        return super().__new__(new_type)

    def __init__(self, errors: list[QueryErrorValue], query: graphql.DocumentNode):
        if not errors:
            msg = "Errors list is empty"
            raise ValueError(msg)
        super().__init__(errors[0])
        self.errors = errors
        self.query = query

    @classmethod
    def from_transport(cls, exc: TransportQueryError, query: graphql.DocumentNode):
        """Create instance from a gql exception."""
        try:
            errors = cattrs.structure(exc.errors, list[QueryErrorValue])
        except (TypeError, KeyError, ValueError):
            return None
        return QueryError(errors, query) if errors else None

    def debug_query(self):
        """Return GraphQL query for debugging purposes."""
        lines = graphql.print_ast(self.query).splitlines()
        # count number of digits from line count
        pad = len(str(len(lines)))
        locations = {loc.line: loc.column for loc in self.errors[0].locations}
        res = []
        for nr, line in enumerate(lines, start=1):
            # prepend line number
            res.append(f"{{:{pad}d}}: {{}}".format(nr, line))
            if nr in locations:
                # add caret below line, pointing to start of error
                res.append(" " * (pad + 1 + locations[nr]) + "^")
        return "\n".join(res)


class ExecError(QueryError):
    """API error from an exec operation."""

    _type = "EXEC_ERROR"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        error: QueryErrorValue = self.args[0]
        ext = error.extensions
        self.command: list[str] = ext["cmd"]
        self.message = error.message
        self.exit_code: int = ext["exitCode"]
        self.stdout: str = ext["stdout"]
        self.stderr: str = ext["stderr"]

    def __str__(self):
        # As a default when just printing the error, include the stdout
        # and stderr for visibility
        return f"{self.message}\nStdout:\n{self.stdout}\nStderr:\n{self.stderr}"
