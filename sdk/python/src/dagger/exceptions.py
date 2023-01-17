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


class ExecuteTimeoutError(ClientError):
    """Timeout while executing a query."""
