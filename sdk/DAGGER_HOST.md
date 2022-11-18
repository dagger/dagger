## NOTE: This is internal documentation and the interfaces described here should be considered highly unstable

## DAGGER_HOST values

### `bin://<path>`

`path` points to an engine-session binary that will be invoked to
start a new session.

If `path` is empty, it defaults to `dagger-engine-session`.

If `path` is not absolute, it will be searched for in `$PATH`.

### `docker-image://<image ref>`

The engine-session binary will be pulled from the provided image via `docker`.

It will then be stored in `$XDG_CACHE_HOME/dagger/dagger-engine-session-<sha>`, where
`sha` is the first 16 chars of the digest of the provided image.

`$XDG_CACHE_HOME` defaults to `~/.cache` if not set in the environment.

Any other binaries prefixed with `dagger-engine-session-` in the cache dir will be deleted
(as they are currently presumed to be from older engine versions).

### `docker-container://<container name>

The engine-session binary will be pulled from the provided docker container, which is
expected to be already running.

The engine-session binary will be stored in a temporary path and deleted when shutdown.

This dagger host value is currently only implemented in the Go SDK and is currently only
intended for testing purposes (e.g. running against a local dev build running in
`test-dagger-engine`).

### `http://<url>

`url` should to point to an tcp endpoint being served by an already running engine-session
binary.

### `unix://path

`path` should to point to an unix socket endpoint being served by an already running
engine-session binary.
