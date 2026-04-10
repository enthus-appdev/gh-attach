// Command gh-attach is a gh CLI extension that uploads images to a
// GitHub PR or issue (or to an ad-hoc key) and prints embeddable
// markdown to stdout. See the repository README for usage details.
package main

import (
	"os"

	"github.com/enthus-appdev/gh-attach/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
