"""Command line interface for the dagger extension runtime."""
import logging
from importlib import import_module
from typing import Annotated

import typer
from click import ClickException
from strawberry import Schema

from dagger.log import configure_logging

from . import _commands as commands
from ._exceptions import SchemaValidationError
from ._server import Server

app = typer.Typer()


@app.command()
def main(
    schema: Annotated[
        bool,
        typer.Option("-schema", help="Save schema to file and exit"),
    ] = False
):
    """Entrypoint for a dagger extension."""
    # Add current directory to path so that the main module can be imported.
    try:
        server = get_server()
    except SchemaValidationError as e:
        # TODO: ClickException just prints the message in a bordered box
        # with title "Error". For some errors we may want a bit more detail.
        raise ClickException(str(e)) from e

    configure_logging(logging.DEBUG if server.debug else logging.INFO)

    if schema:
        server.export_schema()
        raise typer.Exit

    server.execute()


def get_server(module_name: str = "main") -> Server:
    """Get the server instance from the main module."""
    # TODO: Temporarily always on debug during experimental phase.
    # Support user configuration in a `pyproject.toml`.
    debug = True

    try:
        module = import_module(module_name)
    except ModuleNotFoundError as e:
        msg = (
            f'The "{module_name}" module could not be found. '
            f'Did you create a "{module_name}.py" file in the root of your project?'
        )
        raise ClickException(msg) from e

    # These add compatibility for full strawberry control.
    if (server := getattr(module, "server", None)) and isinstance(server, Server):
        return server

    if (schema := getattr(module, "schema", None)) and isinstance(schema, Schema):
        return Server(schema=schema, debug=debug)

    if schema := commands.get_schema(module):
        return Server(schema=schema, debug=debug)

    msg = f'No importable commands were found in module "{module_name}".'
    raise ClickException(msg)
