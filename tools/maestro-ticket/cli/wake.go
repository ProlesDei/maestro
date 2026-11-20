package cli

import (
	"context"
	"errors"
	"os/exec"
	"os/user"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/door"
)

func runWake(ctx context.Context, c *call) error {
	if !door.Wake(c.args) {
		return usagef("wake takes only the optional word store-down")
	}
	account, err := user.Current()
	if err != nil {
		return err
	}
	if account.Username != "maestro" {
		return errors.New("wake requires the configured worker OS user")
	}
	return c.crossDoor(ctx, "maestro-cos", append([]string{"wake"}, c.args...))
}

func (c *call) crossDoor(ctx context.Context, user string, args []string) error {
	argv := append([]string{"-n", "-u", user, "--", door.Path}, args...)
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", argv...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	if args[0] == "cos" {
		cmd.Stdin = c.env.Stdin
	}
	cmd.Stdout, cmd.Stderr = c.env.Stdout, c.env.Stderr
	return cmd.Run()
}
