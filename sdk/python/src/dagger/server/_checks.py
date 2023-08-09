from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver, ResolverFunc

CheckReturnType: TypeAlias = str
CheckResolverFunc: TypeAlias = ResolverFunc[CheckReturnType]


class CheckResolver(Resolver[CheckReturnType]):
    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        check = self.create_kind(dagger.environment_check())
        return env.with_check(check)
