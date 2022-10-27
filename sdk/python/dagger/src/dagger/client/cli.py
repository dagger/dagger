from pathlib import Path
from typing import Optional

import typer
from rich import print

from . import Client
from .codegen import generate as codegen

app = typer.Typer()


@app.callback()
def main():
    """
    Dagger client
    """


@app.command()
def generate(output: Optional[Path] = typer.Option(None, help="File to write generated code")):
    """
    Generate a client for the Dagger API
    """
    with Client() as session:
        if session.client.schema is None:
            raise typer.BadParameter("Schema not initialized. " "Make sure the dagger engine is running.")
        code = codegen(session.client.schema)

    if output is not None:
        output.write_text(code)

        git_attrs = output.with_name(".gitattributes")
        if not git_attrs.exists():
            git_attrs.write_text(f"/{output.name} linguist-generated=true\n")

        print(f"[green]Client generated successfully to[/green] {output} :rocket:")
    else:
        print(code)
