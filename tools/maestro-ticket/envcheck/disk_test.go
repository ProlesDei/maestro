package envcheck

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func TestHeadroom(t *testing.T) {
	small := func(inodes, space float64) Free { return Free{inodes, space, 1000, 1 << 20} }
	by := map[string]Free{
		"/ok": small(50, 50), "/inodes": small(3, 80), "/space": small(80, 3), "/btrfs": small(-1, 50),
		// Under the margin, but over the floor: a big disk with room to spare.
		"/big": {Inodes: 5, Space: 5, InodesLeft: FloorInodes, BytesLeft: FloorBytes},
	}
	free := func(d string) (Free, bool, error) {
		switch d {
		case "/gone":
			return Free{}, false, errors.New("no such file")
		case "/unknown":
			return Free{}, false, nil
		}
		return by[d], true, nil
	}
	for _, tc := range []struct {
		dirs []string
		min  int
		want string // "" for no error, else a word the message holds
	}{
		{[]string{"/ok", "/btrfs", "/gone", "/unknown", "/big"}, 10, ""},
		{[]string{"/ok", "/inodes"}, 10, "/inodes has 3.0% of its inodes free (1000), under the 10% margin"},
		{[]string{"/space"}, 10, "/space has 3.0% of its space free (1 MiB)"},
		{[]string{"/inodes", "/space"}, 0, ""}, // 0 turns it off
		{[]string{"/ok"}, 51, "inodes"},
	} {
		err := Headroom(tc.dirs, tc.min, free)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v at %d%%: %v; want none", tc.dirs, tc.min, err)
		case tc.want != "" && (!errors.Is(err, ticket.ErrLowDisk) || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v at %d%%: %v; want low disk saying %q", tc.dirs, tc.min, err, tc.want)
		}
	}
}

func TestParseMinFree(t *testing.T) {
	for in, want := range map[string]int{"": DefaultMinFree, "0": 0, "25": 25, "99": 99} {
		if got, err := ParseMinFree(in); err != nil || got != want {
			t.Errorf("%q: %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"-1", "100", "10%", "x"} {
		if _, err := ParseMinFree(in); !errors.Is(err, ticket.ErrInvalid) {
			t.Errorf("%q: %v; want invalid", in, err)
		}
	}
}

func TestDiskFreeReadsARealFilesystem(t *testing.T) {
	f, ok, err := DiskFree(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			t.Fatalf("%s: can't tell", runtime.GOOS)
		}
		t.Skip("not measured on " + runtime.GOOS)
	}
	if f.Space < 0 || f.Space > 100 || f.Inodes > 100 || (f.Inodes < 0 && f.Inodes != -1) {
		t.Fatalf("free %+v: want shares in [0, 100], inodes -1 when unknown", f)
	}
}
