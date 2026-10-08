// Command kb is the recorder of Kent the Clerk. Run as: kb <command> ...
//
// The knowledge base it operates on is named by KENT_KB, or found in the current folder or above.
package main

import (
	"os"

	"kenttheclerk/internal/kb"
)

func main() {
	os.Exit(kb.Main(os.Args[1:]))
}
