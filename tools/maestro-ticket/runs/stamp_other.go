//go:build !darwin && !linux

package runs

// Stamp is "" here: the record needs none. Linux ends a run's session
// with its supervisor, and KillRun ends what left its group.
func Stamp(int) string { return "" }

// sessionLeft can't tell the run's session from a later process that
// reuses its pid here, so a supervisor's death ends the run.
func sessionLeft(Record) bool { return false }
