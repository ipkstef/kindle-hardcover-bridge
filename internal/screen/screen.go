// Package screen prints short messages on the Kindle e-ink screen with eips.
// Off the Kindle (no eips), it prints to stdout.
package screen

import (
	"encoding/json"
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

// AlertParams builds the pillowAlert value for the Kindle's generic system
// alert "appAlert1" (title, text and a Close button; simple_alert_config.js,
// FW 5.17.1). autoHideMs > 0 closes it after that time.
func AlertParams(title, text string, autoHideMs int) string {
	type str struct {
		Match   string `json:"matchStr"`
		Replace string `json:"replaceStr"`
	}
	p := map[string]any{"clientParams": map[string]any{
		"alertId": "appAlert1",
		"show":    true,
		"customStrings": []str{
			{"alertTitle", title},
			{"alertText", text},
		},
	}}
	if autoHideMs > 0 {
		p["clientParams"].(map[string]any)["autoHide"] = autoHideMs
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// Alert shows a system alert box over any app (pillow, "appAlert1"). It
// does nothing when LIPC is not available. Device-tested format: probe #3,
// 2026-09-28.
func Alert(title, text string, autoHideMs int) error {
	bin, err := exec.LookPath("lipc-set-prop")
	if err != nil {
		return nil
	}
	return exec.Command(bin, "com.lab126.pillow", "pillowAlert", AlertParams(title, text, autoHideMs)).Run()
}
