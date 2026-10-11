package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/RakhaYandra/tora/internal/cli"
)

func main() {
	if err := cli.New().Execute(); err != nil {
		var ee *cli.ExitError
		if errors.As(err, &ee) {
			fmt.Fprintln(os.Stderr, "tora: "+ee.Msg)
			os.Exit(ee.Code)
		}
		fmt.Fprintln(os.Stderr, "tora: "+err.Error())
		os.Exit(1)
	}
}
