from typing import TypeAlias

from beartype.door import TypeHint
from typing_extensions import override

import dagger

from ._converter import to_graphql_input_representation
from ._resolver import Resolver

FunctionReturnType: TypeAlias = str | dagger.Container | dagger.Directory


class FunctionResolver(Resolver[FunctionReturnType]):
    allowed_return_type: FunctionReturnType

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        func = super().configure_kind(dagger.environment_function())
        for name, param in self.parameters.items():
            arg_type = to_graphql_input_representation(param.signature.annotation)
            is_list = TypeHint(param.signature.annotation).is_bearable(list)
            func = func.with_arg(
                name,
                arg_type,
                is_list,
                is_optional=param.is_optional,
                description=param.description,
            )
        return env.with_function(func)
