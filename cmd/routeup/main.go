// Command routeup is the routeup CLI entry point.
package main

import (
	"errors"
	"os"

	"github.com/mukul-mehta/routeup/internal/cli"
)

func main() {
	err := cli.Execute()
	if err == nil {
		return
	}
	var exit *cli.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.Code)
	}
	cli.PrintError(os.Stderr, err)
	os.Exit(1)
}
