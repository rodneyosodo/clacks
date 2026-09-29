package main

import (
	"fmt"
	"os"

	"github.com/rodneyosodo/clacks/internal/cli"
)

func main() {
	if err := cli.RootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
