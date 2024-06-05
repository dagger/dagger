from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def github_auth(
        self,
        gh_creds: Annotated[dagger.Secret, Doc("GitHub Hosts configuration file")],
    ) -> str:
        """Query the GitHub API"""
        return await (
            dag.container()
            .from_("alpine:3.17")
            .with_exec(["apk", "add", "github-cli"])
            .with_mounted_secret("/root/.config/gh/hosts.yml", gh_creds)
            .with_workdir("/root")
            .with_exec(["gh", "auth", "status"])
            .stdout()
        )
