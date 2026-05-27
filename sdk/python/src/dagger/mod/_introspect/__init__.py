"""Generate-time introspection of a live Dagger module.

This package replaces the AST analyzer (``dagger.mod._analyzer``). Instead
of parsing source statically, it imports the module and reflects on the
live decorated classes (the same machinery the invoke path already uses),
producing:

- ``schematool`` ModuleTypes introspection JSON for the codegen-side
  self-types merge (mirroring the Go SDK), and
- a static entrypoint that the runtime replays without any analysis.

Because it executes the module, it sees runtime-resolved defaults
(``logging.INFO`` -> ``20``) and dynamically-added members, which static
analysis cannot.
"""

from __future__ import annotations

from dagger.mod._introspect._typeref import type_ref

__all__ = ["type_ref"]
