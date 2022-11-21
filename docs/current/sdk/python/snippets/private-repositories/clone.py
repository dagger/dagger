"""
Clone a Private Git Repository and print the content of the README.md file
"""

import sys

import anyio
from rich.console import Console

import dagger

console = Console()


async def private_repo():
    with console.status("Hold on..."):
        async with dagger.Connection() as client:
            repo = (
                client
                # Retrieve the repository
                .git("git@private-repository.git")
                # Select the main branch, and the filesystem tree associated
                .branch("main").tree()
                # Select the README.md file
                .file("README.md")
            )

            # Retrieve the content of the README file
            file = await repo.contents()

    print(file)


if __name__ == "__main__":
    try:
        anyio.run(private_repo)
    except Exception as e:
        print(e, file=sys.stderr)
        sys.exit(1)
