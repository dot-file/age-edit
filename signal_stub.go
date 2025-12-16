//go:build !unix

package main

// handleSignals is a no-op on non-POSIX systems where signal handling is not implemented.
// It returns a function that does nothing.
func handleSignals(save func() error) func() {
	return func() {}
}
