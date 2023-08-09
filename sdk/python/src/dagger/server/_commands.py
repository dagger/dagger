import builtins
from typing import Protocol, TypeAlias, get_type_hints, runtime_checkable

from typing_extensions import override

import dagger

from ._resolver import Resolver, ResolverFunc

CommandReturnType: TypeAlias = str | dagger.Container | dagger.Directory
CommandResolverFunc: TypeAlias = ResolverFunc[CommandReturnType]


@runtime_checkable
class GraphQLNamed(Protocol):
    @classmethod
    def graphql_name(cls) -> str:
        ...


class CommandResolver(Resolver[CommandReturnType]):
    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        command = super().create_kind(dagger.environment_command())
        if ret_type := get_type_hints(self.wrapped_func).get("return"):
            if ret_type is str:
                command = command.with_result_type("String")
            elif isinstance(ret_type, GraphQLNamed):
                    command = command.with_result_type(ret_type.graphql_name())
            else:
                raise TypeError(
                    f"Invalid return type for command {self.name}: {ret_type}"
                )
        return env.with_command(command)

