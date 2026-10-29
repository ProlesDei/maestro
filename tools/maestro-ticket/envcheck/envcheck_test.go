package envcheck

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func TestLocalDirPasses(t *testing.T) {
	dir := t.TempDir()
	checks := Store(dir, false)
	if err := Err(checks); err != nil {
		t.Fatalf("temp dir: %v (%+v)", err, checks)
	}
	if runtime.GOOS == "linux" && checks[1].Detail != "a second writer is refused" {
		t.Fatalf("lock probe didn't run: %+v", checks[1])
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".maestro-lock-probe-*")); len(left) != 0 {
		t.Fatalf("probe left files: %v", left)
	}
}

func fake(t *testing.T, magic int64, lockErr error) {
	t.Helper()
	s, p := statfs, probeLocks
	t.Cleanup(func() { statfs, probeLocks = s, p })
	statfs = func(string) (int64, bool, error) { return magic, true, nil }
	probeLocks = func(string) (bool, error) { return true, lockErr }
}

func TestNetworkAndSharedFilesystemsRefused(t *testing.T) {
	for magic, name := range map[int64]string{0x6969: "nfs", 0xff534d42: "cifs", 0x65735546: "fuse", 0x01021997: "9p", 0x6a656a63: "virtiofs"} {
		fake(t, magic, nil)
		err := Err(Store("/x", false))
		if !errors.Is(err, ticket.ErrInvalid) {
			t.Errorf("%s: err %v; want invalid", name, err)
		}
		if err := Err(Store("/x", true)); err != nil {
			t.Errorf("%s with %s=1: %v", name, AllowEnv, err)
		}
	}
	fake(t, 0xef53, nil) // ext4
	if err := Err(Store("/x", false)); err != nil {
		t.Errorf("ext4: %v", err)
	}
}

func TestBrokenLocksRefused(t *testing.T) {
	fake(t, 0xef53, errors.New("a second write lock on the same byte was granted"))
	if err := Err(Store("/x", false)); !errors.Is(err, ticket.ErrInvalid) {
		t.Fatalf("err %v; want invalid", err)
	}
}

func TestStatfsErrorFails(t *testing.T) {
	if c := Filesystem(filepath.Join(t.TempDir(), "missing")); c.OK && runtime.GOOS == "linux" {
		t.Fatalf("missing dir passed: %+v", c)
	}
}
