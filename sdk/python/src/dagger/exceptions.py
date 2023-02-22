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
        return f"Dagger engine failed to start: {super().__str__()}"


class ClientError(DaggerError):
    """Base class for client errors."""


class InvalidQueryError(ClientError):
    """Misuse of the query builder."""


class ExecuteTimeoutError(ClientError):
    """Timeout while executing a query."""
