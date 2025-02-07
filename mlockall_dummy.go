//go:build ios || js || plan9 || wasip1 || windows

package main

func lockMemory() error {
	return nil
}
