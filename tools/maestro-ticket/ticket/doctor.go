package ticket

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// DoctorChecks runs the doctor checks every store shares, on all its
// tickets at now: leases, stalls, statuses, roles and parked, in that
// order. It reads only each ticket's ID, Status, Role, BlockedFrom,
// Parked, UpdatedAt and Claim.ExpiresAt, so a store may fill just those.
func DoctorChecks(tickets []Ticket, now time.Time) []Check {
	var out []Check
	add := func(name string, ok bool, format string, args ...any) {
		out = append(out, Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}
	tickets = slices.Clone(tickets)
	slices.SortFunc(tickets, func(a, b Ticket) int { return cmp.Compare(a.ID, b.ID) })
	live := func(t Ticket) bool { return t.Claim != nil && t.Claim.ExpiresAt.After(now) }

	var nLive, nExpired int
	for _, t := range tickets {
		switch {
		case live(t):
			nLive++
		case t.Claim != nil:
			nExpired++
		}
	}
	add("leases", true, "%d tickets; %d live claims, %d expired (claimable; reap frees them)", len(tickets), nLive, nExpired)

	// A handoff no one picked up is a stall, not a broken store: report it
	// and pass, so install.sh check still passes on a busy board.
	var stalled []string
	since := now.Add(-DefaultStall)
	for _, t := range tickets {
		if t.Status == StatusInReview && !live(t) && !t.UpdatedAt.After(since) {
			stalled = append(stalled, t.ID.String())
		}
	}
	if len(stalled) == 0 {
		add("stalls", true, "no in_review ticket has waited %d minutes with no one on it", int(DefaultStall.Minutes()))
	} else {
		if len(stalled) > 10 {
			stalled = append(stalled[:10], fmt.Sprintf("and %d more", len(stalled)-10))
		}
		add("stalls", true, "warning: in_review with no one on it for %d minutes or more: %s (maestro-ticket alerts lists them)",
			int(DefaultStall.Minutes()), strings.Join(stalled, ", "))
	}

	// Statuses and roles are checked in Go, not by a schema: list the open
	// tickets whose values this build doesn't know.
	var badStatus, badRole, badParked []string
	for _, t := range tickets {
		if t.Status.Terminal() {
			continue
		}
		if !t.Status.Valid() {
			badStatus = append(badStatus, fmt.Sprintf("%s (%s)", t.ID, t.Status))
		}
		if t.Role != "" && !ValidRole(t.Role) {
			badRole = append(badRole, fmt.Sprintf("%s (%s)", t.ID, t.Role))
		}
		slot := ParkedSlot(t.Status, t.BlockedFrom)
		if p := t.Parked; (slot && !p.Valid()) || (!slot && p != "") {
			badParked = append(badParked, fmt.Sprintf("%s (%s, parked %q)", t.ID, t.Status, string(p)))
		}
	}
	if len(badStatus) == 0 {
		add("statuses", true, "every open ticket has a known status")
	} else {
		add("statuses", false, "unknown status on %s", strings.Join(badStatus, ", "))
	}
	if len(badRole) == 0 {
		add("roles", true, "every open ticket has a known role or none")
	} else {
		add("roles", false, "unknown role on %s (want %s; fix: transition it to another status with --role, then back)",
			strings.Join(badRole, ", "), strings.Join(Roles, ", "))
	}
	if len(badParked) == 0 {
		add("parked", true, "every backlog ticket says why it waits, and no other does")
	} else {
		add("parked", false, "bad parked reason on %s (fix: update <id> --parked <reason> in backlog)", strings.Join(badParked, ", "))
	}
	return out
}
