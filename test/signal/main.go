package main

import (
	"os"
	"syscall"
	"time"
)

func main() {
	path := os.Args[1]

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if _, err := f.WriteString("phase1\n"); err != nil {
		panic(err)
	}

	if err := syscall.Kill(syscall.Getppid(), syscall.SIGUSR1); err != nil {
		panic(err)
	}

	time.Sleep(500 * time.Millisecond)

	if _, err := f.WriteString("phase2\n"); err != nil {
		panic(err)
	}
}
