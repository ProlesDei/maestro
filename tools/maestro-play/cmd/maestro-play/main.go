// Command maestro-play runs Maestro playbooks. See tools/maestro-play/README.md.
package main

import (
	"os"

	"github.com/ProlesDei/maestro/tools/maestro-play/cli"
)

func main() {
	os.Exit(cli.Main(cli.Env{Args: os.Args[1:], Stdout: os.Stdout, Stderr: os.Stderr}))
}
