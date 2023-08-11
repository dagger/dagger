from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver

CheckReturnType: TypeAlias = str | dagger.EnvironmentCheckResult


class CheckResolver(Resolver[CheckReturnType]):
    allowed_return_type: CheckReturnType

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        check = self.configure_kind(dagger.environment_check())
        return env.with_check(check)

    @override
    async def __call__(self, **kwargs) -> CheckReturnType:
        try:
            result = await super().__call__(**kwargs)
        except dagger.QueryError as e:
            if self.return_type is not str:
                raise
            return dagger.environment_check_result(False, str(e))

        if self.return_type is not str:
            return result
        return dagger.environment_check_result(True, result)
