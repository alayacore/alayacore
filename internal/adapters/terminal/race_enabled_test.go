//go:build race

package terminal

// raceDetectorEnabled reports whether this binary was built with -race.
//
// It exists so a cost assertion can say out loud that it cannot be measured in
// this build: runtime.MemStats counts the whole process, and the race
// detector's own runtime allocates while a test runs. See
// requireQuietAllocMeasurement.
const raceDetectorEnabled = true
