package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := command(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func command(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := fmt.Fprintln(out, "Tamizhi local companion\n\n  tamizhi serve [--origin https://your-app] [--data-dir path] [--listen 127.0.0.1:7331]\n  tamizhi chat [--model provider/model] [--permission ask|safe] [--json] your question\n  tamizhi status\n  tamizhi api --config path (configured hosted service)\n\nLocal state defaults to ~/.vezham/tamizhi. Run help on each command for flags.")
		return err
	}
	switch args[0] {
	case "serve":
		return serveLocal(ctx, args[1:], in, out)
	case "chat", "status":
		return localRequest(ctx, args[0], args[1:], in, out, errOut)
	case "api":
		return runHosted(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q; run tamizhi --help", args[0])
	}
}
