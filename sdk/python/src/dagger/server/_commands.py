from typing import TypeAlias

from typing_extensions import override

import dagger

from ._resolver import Resolver

CommandReturnType: TypeAlias = str | dagger.Container | dagger.Directory


class CommandResolver(Resolver[CommandReturnType]):
    allowed_return_type: CommandReturnType

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        command = super().configure_kind(dagger.environment_command())
        return env.with_command(command)
