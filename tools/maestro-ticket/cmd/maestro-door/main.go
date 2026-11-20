// maestro-door is the root-installed sudo target. It constructs the
// environment from the target OS account and accepts only fixed arguments.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/door"
)

func run(ctx context.Context, args []string) error {
	account, err := user.Current()
	if err != nil {
		return err
	}
	var binary string
	var argv []string
	dir := account.HomeDir
	env := []string{"HOME=" + dir, "USER=" + account.Username, "PATH=/usr/local/libexec:/usr/local/bin:/usr/bin:/bin:" + filepath.Join(dir, ".local", "bin"), "LANG=C.UTF-8"}
	switch {
	case account.Username == "maestro-cos" && len(args) > 0 && args[0] == "wake" && door.Wake(args[1:]):
		binary = "/usr/local/libexec/maestro-chief"
		argv = []string{"wake"}
		if len(args) == 2 {
			argv = []string{"store-down"}
		}
	case account.Username == "maestro" && (len(args) == 2 && args[0] == "cos" && door.Cos(args[1]) || len(args) == 1 && args[0] == "startup"):
		binary = "/usr/local/libexec/maestro-ticket"
		cfgPath := filepath.Join(dir, ".config", "maestro-runtime", "worker.json")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return err
		}
		var cfg struct{ Store, Repo string }
		if json.Unmarshal(data, &cfg) != nil || !filepath.IsAbs(cfg.Store) || !filepath.IsAbs(cfg.Repo) {
			return errors.New("invalid worker config")
		}
		dir = cfg.Repo
		env = append(env, "MAESTRO_TICKET_STORE="+cfg.Store, "MAESTRO_TICKET_ACTOR=planner")
		if args[0] == "startup" {
			argv = []string{"recover", "--actor", "planner"}
		} else {
			argv = []string{"cos", args[1]}
		}
	default:
		return errors.New("door refused user or arguments")
	}
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if args[0] == "cos" {
		cmd.Stdin = os.Stdin
	}
	return cmd.Run()
}
func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
