//go:build unix

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// lockMemory locks all current and future memory pages
// to prevent the process from being swapped to disk.
// This protects sensitive data like private keys.
func lockMemory() error {
	if err := unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE); err != nil {
		return fmt.Errorf("failed to lock memory: %w", err)
	}

	return nil
}
