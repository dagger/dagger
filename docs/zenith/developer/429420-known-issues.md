---
slug: /zenith/developer/429420/known-issues
displayed_sidebar: "zenith"
---

# Known Issues

This page describes known issues you may encounter when programming Dagger.

- A module's public fields require a `json:"foo"` tag to be queriable.
- When referencing another module as a local dependency, the dependent module
  must be stored in a sub-directory of the parent module.
- Custom struct types cannot currently be used as parameters.
- Calls to functions across modules will be run exactly _once_ per-session --
  after that, the result will be cached, but only until the next session (a new
  `dagger query`, etc).
  - At some point, we will add more fine-grained cache-control.
- Currently, Go and Python are the only supported languages for module development.
  - Python module development is not yet on par with Go.
  - Node.js modules are not yet available, but under development.
