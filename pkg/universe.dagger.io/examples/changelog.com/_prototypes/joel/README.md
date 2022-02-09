# Low-level port of Changelog.com configuration

This is a port of the changelog.com configuration to the low-level `dagger/engine` APIs.

It currently only uses `engine` (no high-level APIs like `docker`, etc) and, because we don't currently have a way to implement long-running tasks in Europa, it doesn't parts of the config that require them, such as databases for testing. The hope is that we'll be able to figure out a way to implement that soon, and we'll update this config with those capabilities when we are able to.
