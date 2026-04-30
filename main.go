package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jessevdk/go-flags"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		var flagErr *flags.Error
		if errors.As(err, &flagErr) && flagErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		log.Fatal(err)
	}
}
