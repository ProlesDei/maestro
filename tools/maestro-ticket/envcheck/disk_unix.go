//go:build linux || darwin || freebsd

package envcheck

import "golang.org/x/sys/unix"

func platformDiskFree(dir string) (Free, bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Free{}, false, err
	}
	// The field types differ by platform, and FreeBSD's free counts are
	// signed: below zero once root uses its reserve. Clamp, then convert.
	n := func(v int64) uint64 { return uint64(max(v, 0)) }
	blocks, avail := n(int64(st.Blocks)), n(int64(st.Bavail))
	files, ffree := n(int64(st.Files)), n(int64(st.Ffree))
	if blocks == 0 {
		return Free{}, false, nil
	}
	f := Free{Inodes: -1, Space: 100 * float64(avail) / float64(blocks), InodesLeft: ffree, BytesLeft: avail * uint64(st.Bsize)}
	if files > 0 {
		f.Inodes = 100 * float64(ffree) / float64(files)
	}
	return f, true, nil
}
