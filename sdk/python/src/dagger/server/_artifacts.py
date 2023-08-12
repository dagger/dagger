from typing import TypeAlias

from cattrs.preconf.json import JsonConverter
from typing_extensions import override

import dagger

from ._resolver import Resolver

ArtifactReturnType: TypeAlias = dagger.Container | dagger.Directory | dagger.File


class ArtifactResolver(Resolver[ArtifactReturnType]):
    allowed_return_type: ArtifactReturnType

    @override
    def register(self, env: dagger.Environment) -> dagger.Environment:
        artifact = self.configure_kind(dagger.environment_artifact())
        return env.with_artifact(artifact)

    @override
    async def convert_output(
        self, converter: JsonConverter, result: ArtifactReturnType
    ) -> str:
        artifact = self.configure_kind(dagger.environment_artifact())
        conv = {
            dagger.Container: artifact.with_container,
            dagger.Directory: artifact.with_directory,
            dagger.File: artifact.with_file,
        }
        try:
            fn = conv[type(result)]
        except KeyError as e:
            msg = f"Unsupported return type: {type(result)}"
            raise TypeError(msg) from e
        result = fn(result)
        return await super().convert_output(converter, result)
