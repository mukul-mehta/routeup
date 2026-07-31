// Command routeup is the routeup CLI entry point.
package main

import (
	"errors"
	"fmt"
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
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
