from pathlib import Path

import dagger
from dagger.server import command


@command
def test_file(client: dagger.Client, prefix: str) -> dagger.File:
    name = f"{prefix}foo.txt"
    return client.directory().with_new_file(name, "foo\n").file(name)


@command
def test_dir(client: dagger.Client, prefix: str) -> dagger.Directory:
    return (
        client.directory()
        .with_new_directory(f"{prefix}subdir")
        .with_new_file(f"{prefix}subdir/subbar1.txt", "subbar1\n")
        .with_new_file(f"{prefix}subdir/subbar2.txt", "subbar2\n")
        .with_new_file(f"{prefix}bar1.txt", "bar1\n")
        .with_new_file(f"{prefix}bar2.txt", "bar2\n")
    )


@command
def test_imported_project_dir() -> str:
    return "\n".join(str(p) for p in Path().glob("**/*"))
