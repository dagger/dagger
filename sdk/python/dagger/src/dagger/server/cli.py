import asyncio
import logging
import sys

import typer

from ..log import configure_logging
from . import Server

app = typer.Typer()


@app.command()
def main(schema: bool = typer.Option(False, "-schema", help="Save schema to file and exit")):
    """
    Entrypoint for a dagger extension.
    """

    sys.path.insert(0, ".")

    try:
        from main import server
    except ImportError:
        raise typer.BadParameter("No “server: dagger.Server” found in “main” module.")

    if not isinstance(server, Server):
        raise typer.BadParameter("The “server” must be an instance of “dagger.Server”")

    configure_logging(logging.DEBUG if server.debug else logging.INFO)

    if schema:
        server.export_schema()
        raise typer.Exit()

    asyncio.run(server.execute(), debug=server.debug)
