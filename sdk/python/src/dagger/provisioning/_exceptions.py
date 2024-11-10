from dagger._exceptions import DaggerError


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
