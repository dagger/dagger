from pathlib import Path
from typing import Optional

import rich
import typer

from dagger import codegen
from dagger.connectors import Config, get_connector
from dagger.connectors.bin import Engine
from dagger.connectors.docker import EngineFromImage

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

    cfg = Config()

    if cfg.host.scheme == "docker-image":
        with EngineFromImage(cfg) as engine:
            code = generate_code(engine.cfg, sync)
    elif cfg.host.scheme == "bin":
        with Engine(cfg) as engine:
            code = generate_code(engine.cfg, sync)
    else:
        code = generate_code(cfg, sync)

    if output:
        output.write_text(code)
        _update_gitattributes(output)
        rich.print(f"[green]Client generated successfully to[/green] {output} :rocket:")
    else:
        rich.print(code)


def generate_code(cfg: Config, sync: bool) -> str:
    connector = get_connector(cfg)
    transport = connector.make_sync_transport()

    with connector.make_graphql_client(transport) as session:
        if session.client.schema is None:
            raise typer.BadParameter(
                "Schema not initialized. Make sure the dagger engine is running."
            )
        return codegen.generate(session.client.schema, sync)


def _update_gitattributes(output: Path) -> None:
    git_attrs = output.with_name(".gitattributes")
    contents = f"/{output.name} linguist-generated=true\n"

    if git_attrs.exists():
        if contents in (text := git_attrs.read_text()):
            return
        contents = f"{text}{contents}"

    git_attrs.write_text(contents)
