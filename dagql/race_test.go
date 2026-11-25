//go:build race

package dagql

// raceDetector is whether the tests run under the race detector, which
// drops what sync.Pool holds at random: a count of allocation that a pool
// keeps down says nothing there.
const raceDetector = true
