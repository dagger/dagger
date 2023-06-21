import pathlib
from typing import Annotated

from dagger import Client
from dagger.server import command

Repo = Annotated[str, "The git repository to clone"]
Branch = Annotated[str, "The branch to clone"]
Subpath = Annotated[str, "The subpath to clone"]


@command
async def build(client: Client, repo: Repo, branch: Branch, subpath: Subpath) -> str:
    """Build the go binary from the give repo, branch, and subpath."""
    binpath = pathlib.PurePath("bin", subpath).parent
    return await (
        base(client, repo, branch)
        .with_exec(["go", "build", "-v", "-x", "-o", f"{binpath}", f"./{subpath}"])
        .stderr()
    )


@command
async def test(client: Client, repo: Repo, branch: Branch, subpath: Subpath) -> str:
    """Test the go binary from the give repo, branch, and subpath."""
    return await (
        base(client, repo, branch)
        .with_exec(["go", "test", "-v", f"./{subpath}"])
        .stdout()
    )


def base(client: Client, repo: Repo, branch: Branch):
    if not branch:
        branch = "main"
    return (
        client.container()
        .from_("golang:1.20-alpine")
        .with_mounted_cache("/go/pkg/mod", client.cache_volume("go-mod"))
        .with_mounted_cache("/root/.cache/go-build", client.cache_volume("go-build"))
        .with_mounted_directory("/src", client.git(repo).branch(branch).tree())
        .with_workdir("/src")
    )
