class DaggerError(Exception):
    """Base exception for all Dagger exceptions."""


class ProvisionError(DaggerError):
    """Error while provisioning the Dagger engine."""


class ClientError(DaggerError):
    """Base class for client errors."""


class InvalidQueryError(ClientError):
    """Misuse of the query builder."""


class ExecuteTimeoutError(ClientError):
    """Timeout while executing a query."""
