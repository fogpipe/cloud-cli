// Command fpcloud is the Fogpipe CLI. Everything it does lives in pkg/cli,
// which is a package rather than a main so the command tree can be built and
// inspected from outside this binary — the docs pool's check against the real
// cobra registration needs exactly that.
package main

import "github.com/fogpipe/cloud-cli/pkg/cli"

func main() { cli.Execute() }
