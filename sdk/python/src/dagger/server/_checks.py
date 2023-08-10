from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver

CheckReturnType: TypeAlias = str


class CheckResolver(Resolver[CheckReturnType]):
    allowed_return_type: type[CheckReturnType]

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        check = self.configure_kind(dagger.environment_check())
        return env.with_check(check)
