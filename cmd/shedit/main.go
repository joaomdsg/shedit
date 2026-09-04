package main

import (
	"os"

	"github.com/joaomdsg/shedit/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.Env{}))
}
