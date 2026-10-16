package kb

// kb version and kb where.

import (
	"fmt"
	"os"
	"path/filepath"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
)

// productDir is where this version of Kent is installed: the folder holding its executable, under the
// launcher's versions/ folder when it is installed beside the launcher.
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
	if l := os.Getenv("KENT_LAUNCHED"); l != "" {
		println("launched: " + l)
	} else {
		println("launched: directly, not by the launcher")
	}
	root, how := findKBRoot()
	if root == "" {
		println(fmt.Sprintf("knowledge base: none (%s)", how))
		return 1
	}
	println(fmt.Sprintf("knowledge base: %s (%s)", root, how))
	println("pin: " + pinStatus(root))
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
