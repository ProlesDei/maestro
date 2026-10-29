// Command maestro-ticket is the maestro ticket store CLI. Run "maestro-ticket help".
package main

import (
	"os"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
