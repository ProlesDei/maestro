//go:build !linux

package envcheck

func platformLoadAvg() (float64, bool) { return 0, false }
