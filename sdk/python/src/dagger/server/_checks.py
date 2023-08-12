from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver

CheckReturnType: TypeAlias = (
    str | dagger.EnvironmentCheck | dagger.EnvironmentCheckResult
)


class CheckResolver(Resolver[CheckReturnType]):
    allowed_return_type: CheckReturnType

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        check = self.configure_kind(dagger.environment_check())
        return env.with_check(check)

    @override
    async def __call__(self, **kwargs) -> CheckReturnType:
        if self.return_type is str:
            try:
                result = await super().__call__(**kwargs)
                return dagger.environment_check_result().with_success(True).with_output(result)
            except dagger.QueryError as e:
                return dagger.environment_check_result().with_success(False).with_output(str(e))

        return await super().__call__(**kwargs)
