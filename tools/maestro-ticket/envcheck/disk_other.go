//go:build !(linux || darwin || freebsd)

package envcheck

func platformDiskFree(string) (Free, bool, error) { return Free{}, false, nil }
