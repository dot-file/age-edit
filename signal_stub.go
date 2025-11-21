//go:build !unix

package main

func handleSignals(save func() error) func() {
	return func() {}
}
