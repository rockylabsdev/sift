// Command sift drafts your worklogs from the evidence of your work.
package main

import (
	"os"

	"github.com/rockylabsdev/sift/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], cli.OS())) }
