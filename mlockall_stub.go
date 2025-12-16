//go:build !unix

package main

// lockMemory is a no-op on non-POSIX systems where memory locking is not available.
func lockMemory() error {
	return nil
}
