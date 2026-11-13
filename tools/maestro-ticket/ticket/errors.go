package ticket

import "errors"

// Sentinel errors. Drivers wrap these with fmt.Errorf("%w: ...") so callers
// can match with errors.Is and still get a useful message.
var (
	ErrInvalid             = errors.New("invalid request")
	ErrInvalidTransition   = errors.New("transition not allowed")
	ErrForbidden           = errors.New("not allowed for this actor")
	ErrNotFound            = errors.New("not found")
	ErrVersionConflict     = errors.New("version conflict")
	ErrStatusConflict      = errors.New("status conflict")
	ErrClaimed             = errors.New("claimed by another holder")
	ErrLeaseLost           = errors.New("lease lost")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	ErrNoWork              = errors.New("no work available")
	ErrWaitingOnDeps       = errors.New("waiting on dependencies")
	ErrAreaBusy            = errors.New("code area busy")
	ErrHasDependents       = errors.New("open tickets depend on it")
	ErrStopped             = errors.New("refused: the kill switch is on")
	ErrBusy                = errors.New("store busy")
	ErrNoSlot              = errors.New("no free slot")
	ErrLowDisk             = errors.New("low disk")
	ErrStoreTooNew         = errors.New("store is newer than this maestro-ticket")
)

// Exit codes shared by the CLI and documented for bots.
const (
	ExitOK       = 0
	ExitInternal = 1
	ExitUsage    = 2
	ExitNotFound = 3
	ExitConflict = 4
	ExitNoWork   = 5
)

var errorKinds = []struct {
	err  error
	code string
	exit int
}{
	{ErrInvalid, "invalid", ExitUsage},
	{ErrInvalidTransition, "invalid_transition", ExitUsage},
	{ErrForbidden, "forbidden", ExitUsage},
	{ErrNotFound, "not_found", ExitNotFound},
	{ErrVersionConflict, "version_conflict", ExitConflict},
	{ErrStatusConflict, "status_conflict", ExitConflict},
	{ErrClaimed, "claimed", ExitConflict},
	{ErrLeaseLost, "lease_lost", ExitConflict},
	{ErrIdempotencyConflict, "idempotency_conflict", ExitConflict},
	{ErrNoWork, "no_work", ExitNoWork},
	{ErrWaitingOnDeps, "waiting_on_deps", ExitConflict},
	{ErrAreaBusy, "area_busy", ExitConflict},
	{ErrHasDependents, "has_dependents", ExitConflict},
	{ErrStopped, "stopped", ExitConflict},
	{ErrBusy, "busy", ExitConflict},
	{ErrNoSlot, "no_slot", ExitConflict},
	{ErrLowDisk, "low_disk", ExitConflict},
	{ErrStoreTooNew, "store_too_new", ExitUsage},
}

// Classify maps an error to a stable machine code and process exit code.
func Classify(err error) (code string, exit int) {
	if err == nil {
		return "", ExitOK
	}
	for _, k := range errorKinds {
		if errors.Is(err, k.err) {
			return k.code, k.exit
		}
	}
	return "internal", ExitInternal
}
