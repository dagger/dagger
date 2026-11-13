//go:build race

package protocol

// raceEnabled reports a build with the race detector, whose instrumentation
// allocates beside what the code under test does.
const raceEnabled = true
