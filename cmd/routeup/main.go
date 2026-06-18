// Command routeup is the routeup CLI entry point.
package main

import (
	"fmt"
	"os"

	"github.com/mukul-mehta/routeup/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
