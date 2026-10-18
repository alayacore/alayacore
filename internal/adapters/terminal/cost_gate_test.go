package terminal

import "testing"

// requireQuietAllocMeasurement skips a cost assertion in a build whose runtime
// allocates on its own account.
//
// The cost tests below read runtime.MemStats / testing.AllocsPerRun, which
// count the whole process. Under -race the detector's runtime allocates while
// the measurement window is open, and the only bound loose enough to absorb
// that is too loose to be worth asserting. The plain build is quiet, and it is
// what CI runs as `go test ./...`, so that is where the contract is checked;
// this build keeps the behavioral tests and skips the pricing ones. (The
// standard library makes the same split with internal/race.Enabled.)
func requireQuietAllocMeasurement(t *testing.T) {
	t.Helper()
	if raceDetectorEnabled {
		t.Skip("allocation counts are process-wide and the -race runtime allocates too; this contract is asserted in the plain build (go test ./...)")
	}
}
