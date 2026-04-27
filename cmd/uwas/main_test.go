package main

import (
	"os"
	"testing"
)

func TestMainRunsVersionCommand(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"uwas", "version"}
	main()
}
