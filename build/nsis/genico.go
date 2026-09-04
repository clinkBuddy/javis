// Command genico writes the JARVIS .ico used by the NSIS installer and the
// desktop shortcut. Invoked from build/pack.ps1; not shipped.
package main

import (
	"fmt"
	"os"

	"github.com/sjkim/jarvis/internal/appicon"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: genico <out.ico>")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], appicon.ICO(), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
