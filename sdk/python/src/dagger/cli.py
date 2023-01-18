from pathlib import Path
from typing import Optional

import rich
import typer
from graphql import GraphQLSchema

import dagger
from dagger import codegen
from dagger.engine import Engine
from dagger.session import Session

app = typer.Typer()


@app.callback()
def main():
    """Dagger client."""


output_opt = typer.Option(None, help="File to write generated code")
sync_opt = typer.Option(False, help="Generate a client for sync code")


@app.command()
def generate(
    output: Optional[Path] = output_opt,  # noqa: UP007
    sync: bool = sync_opt,
):
    """Generate a client for the Dagger API."""
    # not using `dagger.Connection` because codegen is
    # generating the client that it returns

    schema = _get_schema()
    code = codegen.generate(schema, sync)

    if output:
        output.write_text(code)
        _update_gitattributes(output)
        rich.print(f"[green]Client generated successfully to[/green] {output} :rocket:")
    else:
        rich.print(code)


def _get_schema() -> GraphQLSchema:
    cfg = dagger.Config()
    with Engine(cfg) as conn, Session(conn, cfg) as session:
        if not session.client.schema:
            msg = "Schema not initialized. Make sure the dagger engine is running."
            raise typer.BadParameter(msg)
        return session.client.schema


def _update_gitattributes(output: Path) -> None:
    git_attrs = output.with_name(".gitattributes")
    contents = f"/{output.name} linguist-generated=true\n"

    if git_attrs.exists():
        if contents in (text := git_attrs.read_text()):
            return
        contents = f"{text}{contents}"

    git_attrs.write_text(contents)
