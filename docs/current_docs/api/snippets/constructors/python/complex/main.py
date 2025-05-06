from typing import Annotated

import dagger
from dagger import (
    DefaultPath,
    Doc,
    dag,
    field,
    object_type,
)


@object_type
class Workspace:
    source: Annotated[
        dagger.Directory,
        Doc("The context for the workspace"),
        DefaultPath("/"),
    ]

    token: Annotated[
        dagger.Secret | None,
        Doc("GitHub API token"),
    ] = None

    container: Annotated[
        dagger.Container,
        Doc("The container for the workspace"),
    ] = field(init=False)

    def __post_init__(self):
        self.container = (
            dag.container()
            .from_("python:3.11")
            .with_workdir("/app")
            .with_directory("/app", self.source)
            .with_mounted_cache("/root/.cache/pip", dag.cache_volume("python-pip"))
            .with_exec(["pip", "install", "-r", "requirements.txt"])
        )
