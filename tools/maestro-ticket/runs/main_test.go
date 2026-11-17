package runs

import (
	"os"
	"testing"
	"time"
)

// Tests ring on schedules of milliseconds: a gate of minutes would hold
// each second ring back. The gate tests set their own.
func TestMain(m *testing.M) {
	GateWindow = 200 * time.Millisecond
	os.Exit(m.Run())
}
