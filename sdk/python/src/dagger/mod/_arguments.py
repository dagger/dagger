import inspect
from dataclasses import dataclass, field

from gql.utils import to_camel_case


@dataclass(slots=True, kw_only=True)
class Parameter:
    """Parameter from function signature, in resolver class."""

    name: str
    signature: inspect.Parameter
    description: str | None
    is_optional: bool = field(init=False)
    python_name: str = field(init=False)
    graphql_name: str = field(init=False)

    def __post_init__(self):
        self.is_optional = self.signature.default is not inspect.Signature.empty
        self.python_name = self.signature.name
        self.graphql_name = to_camel_case(self.name)


@dataclass(slots=True, kw_only=True, frozen=True)
class Argument:
    """User defined argument.

    This is used to override a parameter's name in the API, and to give
    it a description.
    """

    name: str | None = None
    description: str | None = None
