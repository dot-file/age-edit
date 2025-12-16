//go:build unix

package main

import (
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

// handleSignals sets up a signal handler for SIGUSR1.
// The handler calls the save function when the signal is received.
// It returns a stop function that should be called to clean up the signal handler.
func handleSignals(save func() error) func() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, unix.SIGUSR1)

	go func() {
		for range c {
			if err := save(); err != nil {
				fmt.Fprintf(os.Stderr, "\r\007age-edit: saving failed: %v\n", err)
			}
		}
	}()

	return func() {
		signal.Stop(c)
		close(c)
	}
}
