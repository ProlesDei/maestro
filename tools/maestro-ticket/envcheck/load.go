package envcheck

// LoadAvg reads the machine's 1-minute load average. ok is false where
// the platform can't tell, and a caller then never refuses for want of a
// number.
func LoadAvg() (load float64, ok bool) { return platformLoadAvg() }
