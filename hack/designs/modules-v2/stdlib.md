# Stdlib

Standard library of reusable modules (Go, Node.js, Python, Netlify, etc.)
that ship with Dagger and provide built-in collection types.

Stdlib is its own track, progressing in stages with gateway dependencies on
the main track:

- **Stage 1** — regular modules, no new infrastructure needed
- **Stage 2** — modules adopt Workspace API (receive workspace, read files/dirs)
- **Stage 3** — modules expose collections (GoModules, GoTests, etc.)

FIXME: detail each stage's scope and what modules are included.
