//go:build unix && !linux && !darwin

package cli

// zombie can't tell on these systems; a zombie counts as alive.
func zombie(int) bool { return false }

// procStamp can't tell a pid's process from a later one here.
func procStamp(int) string { return "" }
