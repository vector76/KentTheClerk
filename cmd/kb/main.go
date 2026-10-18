// Command kb is the recorder of Kent the Clerk. Run as: kb <command> ...
//
// The knowledge bases it operates on are named by KENT_KB, or found in the current folder or above; --kb
// names one among several.
package main

import (
	"os"

	"kenttheclerk/internal/kb"
)

func main() {
	os.Exit(kb.Main(os.Args[1:]))
}
