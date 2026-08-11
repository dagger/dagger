# Dagger Rust SDK authoring macros

This crate is the exact-version procedural-macro companion to `dagger-sdk`. Applications
normally use the `object`, `interface`, `enum_type`, `scalar`, and `methods` attributes
re-exported from `dagger_sdk`; depending on this crate directly is unnecessary.

The macros consume explicit Rust authoring metadata and emit crate-local typed bridges.
They perform no schema discovery, JSON conversion, file access, process execution,
network access, engine work, or runtime registration. Those responsibilities remain in
the SDK's generated code and pure authoring compiler.

The companion and runtime crate must always resolve at the same exact version. This
crate deliberately has no dependency on `dagger-sdk`, which keeps the public package
graph acyclic.
