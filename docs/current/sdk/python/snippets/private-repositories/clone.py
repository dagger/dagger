"""Clone a Private Git Repository and print the content of the README.md file."""

import os
import sys

import anyio
from rich.console import Console

import dagger

console = Console()


async def private_repo():
    with console.status("Hold on..."):
        async with dagger.Connection() as client:
            # Collect value of SSH_AUTH_SOCK env var, to retrieve auth socket path
            ssh_auth_path = os.environ.get("SSH_AUTH_SOCK")

            # Retrieve authentication socket from host
            ssh_agent_socket = client.host().unix_socket(ssh_auth_path)

            repo = (
                client
                # Retrieve the repository
                .git("git@private-repository.git")
                # Select the main branch, and the filesystem tree associated
                .branch("main").tree(None, ssh_agent_socket)
                # Select the README.md file
                .file("README.md")
            )

            # Retrieve the content of the README file
            file = await repo.contents()

    print(file)


if __name__ == "__main__":
    try:
        anyio.run(private_repo)
    except dagger.DaggerError as e:
        print(e, file=sys.stderr)
        sys.exit(1)
