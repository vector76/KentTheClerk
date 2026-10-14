package kb

// kb pin, and the check every act makes against the knowledge base's pin (S-0115).

import (
	"fmt"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/pin"
)

// thisBuild is the pin that would name this build of kb.
func thisBuild() pin.Pin { return pin.Pin{Version: kent.Version(), Hash: kent.Hash()} }

// readPin is the knowledge base's pin; a pin that cannot be read refuses every command that consults it.
func readPin(root string) (pin.Pin, bool) {
	p, ok, err := pin.Read(root)
	if err != nil {
		die(fmt.Sprintf("the knowledge base's pin cannot be read: %v (%s)", err, rule("pinned-version")))
	}
	return p, ok
}

// requirePin refuses an act when the knowledge base is pinned to another version of Kent. It is called by
// every command that writes to the record; reading is never refused.
func requirePin(root string) {
	p, ok := readPin(root)
	if !ok || p.Matches(kent.Version(), kent.Hash()) {
		return
	}
	die(fmt.Sprintf("this knowledge base is pinned to %s, and this kb is %s, so it does not write here (%s). "+
		"To act here, Kent %s is needed; it is installed from Kent the Clerk's releases on GitHub. Tell the "+
		"human which version is needed, and do not install it yourself without their agreement. Moving the "+
		"knowledge base to another version is the human's decision.",
		p, thisBuild(), rule("pinned-version"), p.Version))
}

// pinStatus describes the pin for kb where and kb pin.
func pinStatus(root string) string {
	p, ok := readPin(root)
	switch {
	case !ok:
		return "none (any version of Kent writes here)"
	case p.Matches(kent.Version(), kent.Hash()):
		return p.String() + ", this kb"
	}
	return p.String() + ", not this kb, which is " + thisBuild().String()
}

func runPin(ns argparse.Namespace) int {
	root := repoRoot()
	set, remove := ns.Bool("set"), ns.Bool("remove")
	switch {
	case set && remove:
		die("pass --set or --remove, not both")
	case set:
		old, had := readPin(root)
		if err := pin.Write(root, thisBuild()); err != nil {
			die(fmt.Sprintf("cannot write %s: %v", pin.Path(root), err))
		}
		if had && old != thisBuild() {
			println(fmt.Sprintf("pin moved from %s to %s", old, thisBuild()))
		} else {
			println("pinned to " + thisBuild().String())
		}
		println(fmt.Sprintf("commit %s so everyone who writes here picks it up", pin.FileName))
	case remove:
		if err := pin.Remove(root); err != nil {
			die(fmt.Sprintf("cannot remove %s: %v", pin.Path(root), err))
		}
		println("pin removed; any version of Kent writes here")
	default:
		println("pin: " + pinStatus(root))
	}
	return 0
}

func addPinParser(sub *argparse.Parser) {
	p := sub.AddParser("pin", "show, set or remove the version of Kent this knowledge base is pinned to")
	p.Flag("--set", "pin the knowledge base to this kb's version (the human's decision)")
	p.Flag("--remove", "remove the pin (the human's decision)")
	p.Run = runPin
}
