package sidecar

import (
	"os"
	"testing"
)

// Replay on real .azw3f sidecars from the sleep research (Red Rising, FW
// 5.17.1, text length 934000): before and after a sleep. cc.db said 2.46 %
// and 5.40 %; the sidecar must agree and move forward.
func TestRealSidecarSleep(t *testing.T) {
	read := func(name string) Position {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p, ok := FromDoc(d)
		if !ok || p.LPRPos < 0 {
			t.Fatalf("%s: no lpr: %+v", name, p)
		}
		return p
	}
	a, b := read("start.azw3f"), read("after-sleep.azw3f")
	pa := float64(a.LPRPos) / 934000 * 100
	pb := float64(b.LPRPos) / 934000 * 100
	t.Logf("start %.2f%%, after sleep %.2f%%, saved %s → %s", pa, pb, a.Saved, b.Saved)
	if pb <= pa || pb < 4.9 || pb > 5.9 || !b.Saved.After(a.Saved) {
		t.Fatalf("start %.2f%% after %.2f%%", pa, pb)
	}
}
