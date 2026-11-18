package runs

import (
	"maps"
	"slices"
	"testing"
)

// A run's process is spared only while it serves a process outside the
// run: a listening socket, a connection accepted on it, and a peer no
// process of the run holds. What it started is spared with it.
func TestServing(t *testing.T) {
	socks := map[uint64]sock{
		// 10, an ssh master: listens, and a client outside the run is on.
		1: {listen: true, at: "unix:/s"}, 2: {at: "unix:/s", peer: 900},
		// 20, a dev server: listens, and only the run's own client, 30, is on.
		3: {listen: true, at: "tcp:8080"}, 4: {at: "tcp:8080", peer: 5},
		5: {at: "tcp:50000", peer: 4},
		// 40, a hung test: a client of a server outside the run.
		6: {at: "tcp:50001", peer: 901},
		// 50, an idle daemon: listens, no one on.
		7: {listen: true, at: "tcp:9090"},
	}
	held := map[int][]uint64{10: {1, 2}, 11: nil, 12: nil, 20: {3, 4}, 30: {5}, 40: {6}, 50: {7}}
	// 11 is the master's worker, 12 the worker's child.
	parent := map[int]int{10: 1, 11: 10, 12: 11, 20: 1, 30: 1, 40: 1, 50: 1}
	got := slices.Sorted(maps.Keys(serving(held, socks, parent)))
	if want := []int{10, 11, 12}; !slices.Equal(got, want) {
		t.Errorf("serving = %v, want %v", got, want)
	}
}
