## NOTE: This is internal documentation and the interfaces described here should be considered highly unstable

## DAGGER_RUNNER_HOST values

### `docker-image://<image ref>`

Pull the image and run it with a unique name tied to the pinned
sha of the image. Remove any other containers leftover from
previous executions of the engine at different versions (which
are identified by looking for containers with the prefix
`dagger-engine-`).

### `docker-container://<container name>

Connect to the runner in the provided docker container, which is
expected to be already running.

### `unix://path

`path` should to point to an unix socket endpoint being served by a runner
