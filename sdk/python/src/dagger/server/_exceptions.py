import inspect

from dagger.exceptions import DaggerError


class ServerError(DaggerError):
    ...


class SchemaValidationError(ServerError):
    ...


class BadParameterError(SchemaValidationError):
    def __init__(self, message: str, parameter: inspect.Parameter):
        super().__init__(message)
        self.parameter = parameter
