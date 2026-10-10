import argparse
import json
import pathlib
import sys

import graphql

from codegen import ast, generator

parser = argparse.ArgumentParser(
    prog="python -m codegen", description="Dagger Python SDK"
)


def main():
    subparsers = parser.add_subparsers(
        title="additional commands",
        required=True,
    )
    gen_parser = subparsers.add_parser(
        "generate",
        help="generate a Python client for the API",
    )
    gen_parser.add_argument(
        "-i",
        "--introspection",
        type=pathlib.Path,
        required=True,
        help="path to a .json file holding the introspection result",
    )
    gen_parser.add_argument(
        "-n",
        "--names",
        type=pathlib.Path,
        help=(
            "path to a .json file holding the schema's names formatted by the "
            "engine, as written by `codegen introspect --names-out` with "
            f"--names {generator.NAMES_FORMAT} (defaults to converting names "
            "locally, like for schemas before v1.0.0)"
        ),
    )
    gen_parser.add_argument(
        "-o",
        "--output",
        type=pathlib.Path,
        help=(
            "path to save the generated python module "
            "(defaults to printing it to stdout)"
        ),
    )
    args = parser.parse_args()

    # TODO: Add argument for module init.
    codegen(args.introspection, args.output, args.names)


def codegen(
    introspection: pathlib.Path,
    output: pathlib.Path | None,
    names: pathlib.Path | None = None,
):
    result = json.loads(introspection.read_text())
    schema = graphql.build_client_schema(result)
    ast.insert_stubs(result["__schema"], schema)
    code = generator.generate(
        schema,
        schema_version=result.get("__schemaVersion", ""),
        names=load_names(names),
    )

    if output:
        output.write_text(code)
        sys.stdout.write(f"Client generated successfully to {output}\n")
    else:
        sys.stdout.write(f"{code}\n")


def load_names(path: pathlib.Path | None) -> dict[str, str] | None:
    """Read the snake_case names from a names file.

    A names file maps each ``CASING:ACRONYMS`` format to the schema's names
    formatted in it. It has no formats for schemas without
    ``Query.formatIdentifiers``, which then keep the legacy conversion.
    """
    if path is None:
        return None
    return json.loads(path.read_text()).get(generator.NAMES_FORMAT)
