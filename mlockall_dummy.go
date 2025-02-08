//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package main

func lockMemory() error {
	return nil
}
