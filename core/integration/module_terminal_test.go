package core

// Terminal tests are run directly on the host rather than in exec containers because we want to
// directly interact with the dagger shell tui without resorting to embedding more go code
// into a container for driving it.

// TODO: DNM
