from pathlib import Path
from typing import Optional

import rich
import typer

from dagger import codegen
from dagger.connectors import Config, get_connector

app = typer.Typer()


@app.callback()
def main():
    """
    Dagger client
    """


@app.command()
def generate(
    output: Optional[Path] = typer.Option(None, help="File to write generated code")
):
    """
    Generate a client for the Dagger API
    """
    # not using `dagger.Connection` because codegen is
    # generating the client that it returns
    connector = get_connector(Config())
    transport = connector.make_sync_transport()

    with connector.make_graphql_client(transport) as session:
        if session.client.schema is None:
            raise typer.BadParameter(
                "Schema not initialized. Make sure the dagger engine is running."
            )
        code = codegen.generate(session.client.schema)

    if output is not None:
        output.write_text(code)

        git_attrs = output.with_name(".gitattributes")
        if not git_attrs.exists():
            git_attrs.write_text(f"/{output.name} linguist-generated=true\n")

        rich.print(f"[green]Client generated successfully to[/green] {output} :rocket:")
    else:
        rich.print(code)
