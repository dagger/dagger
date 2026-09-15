from typing import Annotated

import dagger
from dagger import DefaultPath, function, object_type

@object_type
class Test:
    @function
    async def dirs(
        self,
        root: Annotated[dagger.Directory, DefaultPath("/")],
        relative_root: Annotated[dagger.Directory, DefaultPath(".")],
    ) -> list[str]:
        return [
            *(await root.entries()),
            *(await relative_root.entries()),
        ]

    @function
    async def root_dir_path(
        self,
        backend: Annotated[dagger.Directory, DefaultPath("/backend")],
        frontend: Annotated[dagger.Directory, DefaultPath("/frontend")],
        mod_src_dir: Annotated[dagger.Directory, DefaultPath("/dagger/sub")],
    ) -> list[str]:
        return [
            *(await backend.entries()),
            *(await frontend.entries()),
            *(await mod_src_dir.entries()),
        ]

    @function
    async def relative_dir_path(
        self,
        mod_src_dir: Annotated[dagger.Directory, DefaultPath("./dagger/sub")],
        backend: Annotated[dagger.Directory, DefaultPath("./backend")],
    ) -> list[str]:
        return [
            *(await mod_src_dir.entries()),
            *(await backend.entries()),
        ]

    @function
    async def files(
        self,
        license: Annotated[dagger.File, DefaultPath("/LICENSE")],
        index: Annotated[dagger.File, DefaultPath("./dagger.json")],
    ) -> list[str]:
        return [
            await license.name(),
            await index.name(),
        ]
