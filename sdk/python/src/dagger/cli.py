from pathlib import Path
from typing import Optional

import rich
import typer

import dagger
from dagger import codegen
from dagger.connector import Connector
from dagger.engine import get_engine

app = typer.Typer()


@app.callback()
def main():
    """
    Dagger client
    """


@app.command()
def generate(
    output: Optional[Path] = typer.Option(None, help="File to write generated code"),
    sync: Optional[bool] = typer.Option(False, help="Generate a client for sync code"),
):
    """
    Generate a client for the Dagger API
    """
    # not using `dagger.Connection` because codegen is
    # generating the client that it returns

    cfg = dagger.Config()

    with get_engine(cfg):
        connector = Connector(cfg)
        gql_transport = connector.make_sync_transport()
        gql_client = connector.make_graphql_client(gql_transport)

        with gql_client as session:
            if session.client.schema is None:
                raise typer.BadParameter(
                    "Schema not initialized. Make sure the dagger engine is running."
                )
            code = codegen.generate(session.client.schema, sync)

    if output:
        output.write_text(code)
        _update_gitattributes(output)
        rich.print(f"[green]Client generated successfully to[/green] {output} :rocket:")
    else:
        rich.print(code)


def _update_gitattributes(output: Path) -> None:
    git_attrs = output.with_name(".gitattributes")
    contents = f"/{output.name} linguist-generated=true\n"

    if git_attrs.exists():
        if contents in (text := git_attrs.read_text()):
            return
        contents = f"{text}{contents}"

    git_attrs.write_text(contents)
