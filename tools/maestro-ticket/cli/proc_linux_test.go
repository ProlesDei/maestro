package cli

import "testing"

// Lines from real processes: a sleeper, a reaped-never zombie, and a
// process whose main thread called pthread_exit while a worker runs.
func TestZombieStat(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{"42 (sleep) S 1 42 42 0 -1 4194304 90 0 0 0 0 0 0 0 20 0 1 0 1 5 1 18446744073709551615", false},
		{"43 (sleep) Z 42 42 42 0 -1 4227084 90 0 0 0 0 0 0 0 20 0 1 0 1 0 0 18446744073709551615", true},
		{"44 (t) Z 1 44 44 0 -1 4194560 21 0 0 0 0 0 0 0 20 0 2 0 1 0 0 18446744073709551615", false},
		{"45 (a) b) Z) Z 1 45 45 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 1 0 0 0", true},
		{"46 (short) Z", false},
	} {
		if got := zombieStat([]byte(c.line)); got != c.want {
			t.Errorf("zombieStat(%q) = %v; want %v", c.line, got, c.want)
		}
	}
}
