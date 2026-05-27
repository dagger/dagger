"""CLI for generate-time module introspection.

Run as ``python -m dagger.mod._introspect <command>``. Imports the user
module (no engine connection) and emits artifacts the codegen pipeline
consumes.

Subcommands:
    emit        Write the module's schematool ModuleTypes JSON.
    merge       Merge module types into the base schema (engine schematool).
    entrypoint  Write the static _dagger_main.py entrypoint.
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


def _entrypoint(args: argparse.Namespace) -> None:
    from dagger.mod._introspect.entrypoint import render_entrypoint
    from dagger.mod._module import MAIN_OBJECT, MODULE_NAME
    from dagger.mod.cli import load_module

    module = load_module()
    source = render_entrypoint(
        module,
        main_object_name=args.main_object or MAIN_OBJECT,
        module_name=args.module_name or MODULE_NAME,
    )

    if args.output:
        Path(args.output).write_text(source, encoding="utf-8")
    else:
        sys.stdout.write(source)


async def _merge_async(base: str, module_types: str, module_name: str) -> str:
    import dagger
    from dagger import dag

    async with await dagger.connect():
        merged = await (
            dag.schema(dagger.JSON(base))
            .merge(dagger.JSON(module_types), module_name)
            .contents()
        )
    return str(merged)


def _merge(args: argparse.Namespace) -> None:
    import anyio

    base = Path(args.introspection_json).read_text(encoding="utf-8")
    module_types = Path(args.module_types).read_text(encoding="utf-8")
    merged = anyio.run(_merge_async, base, module_types, args.module_name)
    Path(args.output).write_text(merged, encoding="utf-8")


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

    entrypoint = subparsers.add_parser(
        "entrypoint", help="Emit the static _dagger_main.py entrypoint."
    )
    entrypoint.add_argument(
        "--main-object", default=None, help="Main object name (default: env)."
    )
    entrypoint.add_argument(
        "--module-name", default=None, help="Module name (default: env)."
    )
    entrypoint.add_argument(
        "--output", default=None, help="Output path (default: stdout)."
    )
    entrypoint.set_defaults(func=_entrypoint)

    merge = subparsers.add_parser(
        "merge",
        help="Merge module types into the base schema via the engine schematool.",
    )
    merge.add_argument("--introspection-json", required=True, help="Base schema JSON.")
    merge.add_argument("--module-types", required=True, help="ModuleTypes JSON path.")
    merge.add_argument("--module-name", required=True, help="Module name.")
    merge.add_argument("--output", required=True, help="Merged schema output path.")
    merge.set_defaults(func=_merge)

    args = parser.parse_args(argv)
    args.func(args)


if __name__ == "__main__":
    main()
