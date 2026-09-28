// Package screen prints short messages on the Kindle e-ink screen with eips.
// Off the Kindle (no eips), it prints to stdout.
package screen

import (
	"fmt"
	"os/exec"
)

// Screen writes lines from a start row.
type Screen struct {
	Row  int // first text row
	eips string
}

// New finds eips.
func New(row int) *Screen {
	p, _ := exec.LookPath("eips")
	return &Screen{Row: row, eips: p}
}

// Show prints lines, one per row, and pads each line to clear old text.
func (s *Screen) Show(lines ...string) {
	for i, l := range lines {
		fmt.Println(l)
		if s.eips != "" {
			_ = exec.Command(s.eips, "1", fmt.Sprint(s.Row+i), fmt.Sprintf("%-46s", l)).Run()
		}
	}
}
