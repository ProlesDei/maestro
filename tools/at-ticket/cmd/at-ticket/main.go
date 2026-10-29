// Command at-ticket is the maestro ticket store CLI. Run "at-ticket help".
package main

import (
	"os"

	"github.com/ProlesDei/maestro/tools/at-ticket/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
