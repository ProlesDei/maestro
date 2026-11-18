// Package storetest is the conformance suite every ticket.Store runs: the
// tests of the Store contract that hold for any driver, moved out of
// sqlitestore so a second store passes the same ones. A driver calls Run
// from one test of its own and keeps its driver-only tests (files, locks,
// migrations, its clock) beside it.
//
// The suite relies on these, beyond what ticket.Store documents:
//   - Ids are dense. In a fresh store, tickets get ids 1, 2, 3 in the order
//     they are created, and so do comments; event seqs start at 1 and rise
//     by one per event; a ticket's first lease token is 1 and each claim
//     adds one.
//   - Every client reads the clock the factory is given, to the
//     millisecond, so a test moves time for all of them at once.
//   - Clients opened on one store see each other's commits at once, as
//     separate processes would.
package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

// Factory makes one fresh, empty store for test t and returns open, which
// opens a client on it. Every client reads now for the time. Each call to
// open gives a new client with its own connections, as another process
// would have; the suite closes each one. The factory removes the store
// itself when t ends, with t.Cleanup.
type Factory func(t *testing.T, now func() time.Time) (open func() (ticket.Store, error))

// ReapTickets is how many expired leases the reap test frees in one call.
// A store's reap batch must be under half of it, so the test crosses two
// batch boundaries.
const ReapTickets = 207

// ForgetKeys is how many old idempotency keys the forget test has one reap
// delete. A store's key batch must be under half of it, and the keys one
// reap may delete at least it, so the test crosses two batch boundaries.
const ForgetKeys = 207

// Clock is a fake clock that tests move by hand. It is safe for
// concurrent use.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// NewClock returns a clock set to the suite's start time.
func NewClock() *Clock { return &Clock{t: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)} }

// Now returns the clock's time.
func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

// Advance moves the clock on by d.
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// RunAll runs fn(i) on n goroutines released together, and waits for all.
func RunAll(n int, fn func(i int)) {
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			start.Wait()
			fn(i)
		}()
	}
	start.Done()
	done.Wait()
}

// Run runs the suite on stores f makes, each test as a subtest.
func Run(t *testing.T, f Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, f Factory)
	}{
		// Tickets, claims and the workflow.
		{"CreateGetList", testCreateGetList},
		{"ListOrderPriorityThenID", testListOrderPriorityThenID},
		{"ClaimNextOrderAndFilters", testClaimNextOrderAndFilters},
		{"ClaimByID", testClaimByID},
		{"LeaseExpiryAndHeartbeat", testLeaseExpiryAndHeartbeat},
		{"HeartbeatAfterExpiry", testHeartbeatAfterExpiry},
		{"HeartbeatFencedByToken", testHeartbeatFencedByToken},
		{"Release", testRelease},
		{"TransitionChecks", testTransitionChecks},
		{"TerminalTransitionClearsLease", testTerminalTransitionClearsLease},
		{"FullWorkflow", testFullWorkflow},
		{"Comments", testComments},
		{"Idempotency", testIdempotency},
		{"CreateReplayShowsCurrentState", testCreateReplayShowsCurrentState},
		{"FailedWriteRollsBack", testFailedWriteRollsBack},
		{"EventsCursor", testEventsCursor},
		{"BlockedReturnsOnlyToOrigin", testBlockedReturnsOnlyToOrigin},
		{"TriesCountReviewSendBacks", testTriesCountReviewSendBacks},
		{"ScoutSendBackIsNotATry", testScoutSendBackIsNotATry},
		{"BacklogAndScoutTickets", testBacklogAndScoutTickets},
		{"StaleSessionIsFenced", testStaleSessionIsFenced},
		{"ReclaimFencesOldToken", testReclaimFencesOldToken},
		{"ClaimReplayAfterLeaseLoss", testClaimReplayAfterLeaseLoss},
		{"QALeadOrPlannerSetsShipDone", testQALeadOrPlannerSetsShipDone},
		{"ScoutDoneByOwner", testScoutDoneByOwner},
		{"ExpiredLeaseLogsEvent", testExpiredLeaseLogsEvent},
		// Dependencies and areas.
		{"ClaimWaitsOnDeps", testClaimWaitsOnDeps},
		{"DepsValidation", testDepsValidation},
		{"ListReadyMatchesClaimNext", testListReadyMatchesClaimNext},
		{"ClaimSkipsBusyAreas", testClaimSkipsBusyAreas},
		{"BacklogClaimCantStartPastChecks", testBacklogClaimCantStartPastChecks},
		{"UpdateAreasChecksLiveClaims", testUpdateAreasChecksLiveClaims},
		{"OnlyWorkClaimsUseAreasAndDeps", testOnlyWorkClaimsUseAreasAndDeps},
		{"StartWaitsOnDeps", testStartWaitsOnDeps},
		{"NoOpenTicketWaitsOnCancelled", testNoOpenTicketWaitsOnCancelled},
		// Leases.
		{"UnclaimedWritesLogExpiredLease", testUnclaimedWritesLogExpiredLease},
		{"LeaseExpiriesCount", testLeaseExpiriesCount},
		{"ReapBatches", testReapBatches},
		{"ReapForgetsOldKeys", testReapForgetsOldKeys},
		{"LeaseExpiresAtExactMillisecond", testLeaseExpiresAtExactMillisecond},
		{"HeartbeatNeverShortensLease", testHeartbeatNeverShortensLease},
		{"KeyedWriteReplaysUnderNewToken", testKeyedWriteReplaysUnderNewToken},
		{"ReleaseDiedCountsExpiry", testReleaseDiedCountsExpiry},
		// Parked, goals and tiers.
		{"BacklogNeedsAReason", testBacklogNeedsAReason},
		{"Goal", testGoal},
		{"Tier", testTier},
		// Runs.
		{"LogRunKeepsTheSessionsNewestTotal", testLogRunKeepsTheSessionsNewestTotal},
		{"LogRunRefuses", testLogRunRefuses},
		{"RunsCarryTheTicketsOrigin", testRunsCarryTheTicketsOrigin},
		{"RunsCountTowardTheRewrite", testRunsCountTowardTheRewrite},
		{"UpdateSplitFromLinksARefile", testUpdateSplitFromLinksARefile},
		{"UpdateSplitFromCarriesTheGoal", testUpdateSplitFromCarriesTheGoal},
		// Watch: revivals, launches, ETAs, stalls.
		{"ClaimCountsRevivals", testClaimCountsRevivals},
		{"LaunchesEndOnce", testLaunchesEndOnce},
		{"ETAs", testETAs},
		{"DoctorFlagsStalls", testDoctorFlagsStalls},
		// Many clients at once.
		{"RaceClaimNext", testRaceClaimNext},
		{"RaceNoLostComments", testRaceNoLostComments},
		{"RaceOptimisticTransition", testRaceOptimisticTransition},
		{"RaceIdempotentCreate", testRaceIdempotentCreate},
		{"RaceWritesAfterReclaim", testRaceWritesAfterReclaim},
		{"RaceExpiryVsHeartbeat", testRaceExpiryVsHeartbeat},
		{"RaceAreaLocks", testRaceAreaLocks},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, f) })
	}
}

// openMany opens n clients with open, and closes each when t ends.
func openMany(t *testing.T, open func() (ticket.Store, error), n int) []ticket.Store {
	t.Helper()
	stores := make([]ticket.Store, n)
	for i := range stores {
		s, err := open()
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
		t.Cleanup(func() { s.Close() })
	}
	return stores
}

// newStore opens one client on a fresh store, on a fake clock.
func newStore(t *testing.T, f Factory) (ticket.Store, *Clock) {
	t.Helper()
	clk := NewClock()
	return openMany(t, f(t, clk.Now), 1)[0], clk
}

func mustCreate(t *testing.T, s ticket.Store, title, role string, prio int) ticket.Ticket {
	t.Helper()
	tk, err := s.Create(context.Background(), ticket.CreateRequest{Title: title, Role: role, Priority: &prio, Actor: "cos"})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func create(t *testing.T, s ticket.Store, req ticket.CreateRequest) ticket.Ticket {
	t.Helper()
	tk, err := s.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v; want %v", err, want)
	}
}

// move runs one unclaimed transition and fails the test on error.
func move(t *testing.T, s ticket.Store, id ticket.ID, to ticket.Status) ticket.Ticket {
	t.Helper()
	got, err := s.Transition(context.Background(), ticket.TransitionRequest{ID: id, To: to, Actor: "test"})
	if err != nil {
		t.Fatalf("%s -> %s: %v", id, to, err)
	}
	return got
}

func shipToDone(t *testing.T, s ticket.Store, id ticket.ID) {
	t.Helper()
	for _, to := range []ticket.Status{ticket.StatusInProgress, ticket.StatusInReview} {
		move(t, s, id, to)
	}
	if _, err := s.Transition(context.Background(), ticket.TransitionRequest{ID: id, To: ticket.StatusDone, ActorRole: ticket.RoleQALead}); err != nil {
		t.Fatal(err)
	}
}

func eventTypes(t *testing.T, s ticket.Store, since int64) []string {
	t.Helper()
	evs, err := s.Events(context.Background(), ticket.EventFilter{Since: since})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	return types
}

func mustSetETA(t *testing.T, s ticket.Store, goal string, due time.Time) {
	t.Helper()
	if _, err := s.SetETA(context.Background(), ticket.SetETARequest{Goal: goal, Due: due}); err != nil {
		t.Fatal(err)
	}
}
