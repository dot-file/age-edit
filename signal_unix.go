//go:build unix

package main

import (
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

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
