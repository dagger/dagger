from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver

FunctionReturnType: TypeAlias = str | dagger.Container | dagger.Directory


class FunctionResolver(Resolver[FunctionReturnType]):
    allowed_return_type: type[FunctionReturnType]

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        func = super().configure_kind(dagger.environment_function())
        return env.with_function(func)
