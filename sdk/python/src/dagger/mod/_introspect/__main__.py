"""CLI for generate-time module introspection.

Run as ``python -m dagger.mod._introspect <command>``. Imports the user
module (no engine connection) and emits artifacts the codegen pipeline
consumes.

Subcommands:
    emit    Write the module's schematool ModuleTypes JSON.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from dagger.mod._introspect.serialize import live_to_introspection_json


def _emit(args: argparse.Namespace) -> None:
    # Imported lazily so ``--help`` doesn't pull the module loader.
    from dagger.mod._module import MAIN_OBJECT, MODULE_NAME
    from dagger.mod.cli import load_module

    module = load_module()
    response = live_to_introspection_json(
        module,
        main_object_name=args.main_object or MAIN_OBJECT,
        module_name=args.module_name or MODULE_NAME,
    )
    output = json.dumps(response, indent=2)

    if args.output:
        Path(args.output).write_text(output, encoding="utf-8")
    else:
        sys.stdout.write(output)


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(prog="dagger.mod._introspect")
    subparsers = parser.add_subparsers(dest="command", required=True)

    emit = subparsers.add_parser(
        "emit", help="Emit the module's schematool ModuleTypes JSON."
    )
    emit.add_argument(
        "--main-object", default=None, help="Main object name (default: env)."
    )
    emit.add_argument("--module-name", default=None, help="Module name (default: env).")
    emit.add_argument("--output", default=None, help="Output path (default: stdout).")
    emit.set_defaults(func=_emit)

    args = parser.parse_args(argv)
    args.func(args)


if __name__ == "__main__":
    main()
