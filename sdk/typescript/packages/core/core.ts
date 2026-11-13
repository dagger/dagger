// A module's copy of client.gen.ts imports the runtime from "./core.js", the
// library bundle beside it. Inside the library's own source the same specifier
// reaches the session runtime here, so the generated bindings read the same in
// both places and the library's file can be shipped into a module as it is.
//
// By package name, not `../session/context.js`: core is its own npm package, so a
// relative path out of it emits verbatim into core/dist/core.js and resolves to
// core/session/context.js — nothing. The specifier has to be one that still works
// from inside the published tarball.
export { Context, BaseClient } from "@dagger.io/session"
