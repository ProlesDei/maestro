//go:build linux || darwin || freebsd

package envcheck

import "golang.org/x/sys/unix"

func platformDiskFree(dir string) (Free, bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Free{}, false, err
	}
	// The field types differ by platform; the casts make one body fit all.
	blocks, avail := uint64(st.Blocks), uint64(st.Bavail)
	files, ffree := uint64(st.Files), uint64(st.Ffree)
	if blocks == 0 {
		return Free{}, false, nil
	}
	f := Free{Inodes: -1, Space: 100 * float64(avail) / float64(blocks)}
	if files > 0 {
		f.Inodes = 100 * float64(ffree) / float64(files)
	}
	return f, true, nil
}
