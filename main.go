package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

func main() {
	dir := flag.String("dir", ".", "Directory to scan for audio files")
	lowPower := flag.Bool("lowpower", false, "Reduce CPU usage (disable spectrum/EQ, lower resample quality)")
	flag.Parse()

	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-dir path]\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}

	p := tea.NewProgram(initialModel(*dir, *lowPower))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
