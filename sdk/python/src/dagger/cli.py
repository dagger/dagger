from pathlib import Path
from typing import Optional

import rich
import typer

from dagger import codegen
from dagger.connectors import Config, get_connector
from dagger.connectors.docker import Engine

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
    cfg = Config()

    if cfg.host.scheme == "docker-image":
        with Engine(cfg) as engine:
            code = generate_code(engine.cfg)
    else:
        code = generate_code(engine.cfg)

    if output is not None:
        output.write_text(code)

        git_attrs = output.with_name(".gitattributes")
        if not git_attrs.exists():
            git_attrs.write_text(f"/{output.name} linguist-generated=true\n")

        rich.print(f"[green]Client generated successfully to[/green] {output} :rocket:")
    else:
        rich.print(code)


def generate_code(cfg: Config) -> str:
    connector = get_connector(cfg)
    transport = connector.make_sync_transport()

    with connector.make_graphql_client(transport) as session:
        if session.client.schema is None:
            raise typer.BadParameter(
                "Schema not initialized. Make sure the dagger engine is running."
            )
        return codegen.generate(session.client.schema)
