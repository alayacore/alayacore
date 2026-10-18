//go:build !race

package terminal

// raceDetectorEnabled reports whether this binary was built with -race.
// See race_enabled_test.go.
const raceDetectorEnabled = false
