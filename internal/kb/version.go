package kb

// kb version and kb where (kb/version.py, kb/where.py).

import (
	"fmt"
	"os"
	"path/filepath"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
)

// productDir is where Kent is installed: the folder holding the kb executable, beside which the rules
// are shipped.
func productDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

func runVersion(ns argparse.Namespace) int {
	println("system_version: " + kent.Version())
	println("system_hash: " + kent.Hash())
	if kent.Dev() {
		println("system_build: dev")
		println("system_commit: " + kent.Commit())
		println(fmt.Sprintf("system_dirty: %t", kent.Dirty()))
	}
	if ns.Bool("files") {
		println(kent.FilesHeading)
		for _, f := range kent.Files() {
			println("  " + f)
		}
	}
	return 0
}

func runWhere(ns argparse.Namespace) int {
	println("kent: " + productDir())
	root, how := findKBRoot()
	if root == "" {
		println(fmt.Sprintf("knowledge base: none (%s)", how))
		return 1
	}
	println(fmt.Sprintf("knowledge base: %s (%s)", root, how))
	return 0
}

func addVersionParser(sub *argparse.Parser) {
	p := sub.AddParser("version", "print system version and content hash")
	p.Flag("--files", "also list the files the hash covers")
	p.Run = runVersion
}

func addWhereParser(sub *argparse.Parser) {
	p := sub.AddParser("where", "print where Kent is installed and which knowledge base kb uses")
	p.Run = runWhere
}
