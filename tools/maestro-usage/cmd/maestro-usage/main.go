// Command maestro-usage records harness usage and gates budgets with no
// tokens spent. Run "maestro-usage help".
package main

import (
	"os"

	"github.com/ProlesDei/maestro/tools/maestro-usage/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
