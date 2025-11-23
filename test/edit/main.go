package main

import (
	"os"
	"time"
)

func main() {
	args := os.Args[1:]
	readOnly := false

	if args[0] == "--read-only" {
		readOnly = true
		args = args[1:]
	}

	f, err := os.OpenFile(args[0], os.O_RDONLY, 0)
	if err != nil {
		panic(err)
	}
	_ = f.Close()

	time.Sleep(100 * time.Millisecond)

	if readOnly {
		return
	}

	f, err = os.OpenFile(args[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if _, err := f.WriteString("edit\n"); err != nil {
		panic(err)
	}
}
