// maestro-chief runs as the chief OS user. It reads its own private
// connection and takes its session identity from the current T3 thread.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/chief"
)

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: maestro-chief register|assign|draft|publish|read|instruction|result|direction|propose|claim|question|answer|rule|ack < request.json")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	cfg, err := chief.ReadConfig(filepath.Join(config, "maestro-chief", "board.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if os.Args[1] == "store-down" {
		return chief.SendWake(ctx, cfg.T3Base, cfg.FaultThread)
	}
	service, err := chief.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer service.Close(ctx)
	if os.Args[1] == "prepare" {
		return chief.DescribeError(service.Prepare(ctx))
	}
	if os.Args[1] == "wake" {
		return service.Wake(ctx, cfg.T3Base)
	}
	dec := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	dec.DisallowUnknownFields()
	var req chief.Request
	if err = dec.Decode(&req); err != nil {
		return errors.New("invalid chief request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("expected one request")
	}
	var out any
	thread := os.Getenv("T3CODE_THREAD_ID")
	if os.Args[1] == "answer" {
		out, err = service.Capture(ctx, cfg.T3Base, thread, req)
	} else {
		out, err = service.Apply(ctx, thread, os.Args[1], req)
	}
	if err != nil {
		return chief.DescribeError(err)
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
