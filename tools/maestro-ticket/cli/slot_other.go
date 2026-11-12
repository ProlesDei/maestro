//go:build !unix

package cli

import (
	"fmt"
	"os"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

func tryLock(*os.File) (bool, error) {
	return false, fmt.Errorf("%w: slot needs a unix system", ticket.ErrInvalid)
}
