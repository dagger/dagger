import logging
import sys
from typing import Annotated

import anyio
import typer

from dagger.log import configure_logging

from . import Server

app = typer.Typer()


@app.command()
def main(
    schema: Annotated[
        bool,
        typer.Option("-schema", help="Save schema to file and exit"),
    ] = False
):
    """Entrypoint for a dagger extension."""
    sys.path.insert(0, ".")

    try:
        from main import server
    except ImportError as e:
        msg = "No “server: dagger.Server” found in “main” module."
        raise typer.BadParameter(msg) from e

    if not isinstance(server, Server):
        msg = "The “server” must be an instance of “dagger.Server”"
        raise typer.BadParameter(msg)

    configure_logging(logging.DEBUG if server.debug else logging.INFO)

    if schema:
        server.export_schema()
        raise typer.Exit

    anyio.run(server.execute)
