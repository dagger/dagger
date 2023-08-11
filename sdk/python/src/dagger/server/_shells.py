from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver

ShellReturnType: TypeAlias = str


class ShellResolver(Resolver[ShellReturnType]):
    allowed_return_type: ShellReturnType

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        shell = self.configure_kind(dagger.environment_shell())
        return env.with_shell(shell)
