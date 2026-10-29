//go:build !linux

package envcheck

func platformStatfs(string) (int64, bool, error) { return 0, false, nil }

func platformProbeLocks(string) (bool, error) { return false, nil }
