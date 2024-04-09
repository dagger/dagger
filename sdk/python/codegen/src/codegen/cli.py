import argparse
import json
import pathlib
import sys

import graphql

from codegen import generator

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
    codegen(args.introspection, args.output)


def codegen(introspection: pathlib.Path, output: pathlib.Path | None):
    result = json.loads(introspection.read_text())
    schema = graphql.build_client_schema(result)
    code = generator.generate(schema)

    if output:
        output.write_text(code)
        sys.stdout.write(f"Client generated successfully to {output}\n")
    else:
        sys.stdout.write(f"{code}\n")
