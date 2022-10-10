import logging
import sys

import click

from .log import configure_logging
from .server import Server


@click.command(short_help="Entrypoint for a dagger extension")
@click.option("-schema", is_flag=True, help="Save schema to file")
def run(schema: bool):
    sys.path.insert(0, ".")

    try:
        from main import server
    except (ImportError, AttributeError) as exc:
        raise click.BadArgumentUsage(
            "No “server: dagger.Server” found in “main” module."
        )

    if not isinstance(server, Server):
        raise click.BadArgumentUsage(
            "The “server” must be an instance of “dagger.Server”"
        )

    configure_logging(logging.DEBUG if server.debug else logging.INFO)

    if schema:
        server.export_schema()
        return

    server.execute()
