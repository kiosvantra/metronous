package main

import (
	"os"

	"github.com/kiosvantra/metronous/cmd/metronous/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
