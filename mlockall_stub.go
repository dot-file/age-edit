//go:build !unix

package main

func lockMemory() error {
	return nil
}
