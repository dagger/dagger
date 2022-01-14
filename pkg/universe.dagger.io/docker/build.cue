package docker

import (
)
// "universe.dagger.io/docker/build"
// FIXME: this causes a circular package dependency.
//    Cue should handle it because there is no field-level cycle,
//    but it doesn't seem to (cue eval hangs).

// Build a Docker container with a pure CUE API.
// See universe.dagger.io/docker/build for more details
// #Build: build.#Build
