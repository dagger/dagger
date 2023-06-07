from dagger.exceptions import DaggerError


class ServerError(DaggerError):
    ...


class SchemaValidationError(ServerError):
    ...
