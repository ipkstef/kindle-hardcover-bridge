package syncer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/sidecar"
)

// Percent sources, in priority order (user decision 2026-09-28: the sidecar
// is only the third choice; APNX page maps are not used).
const (
	SrcCCPercent  = "cc.db percent"       // p_percentFinished (confirmed on device)
	SrcCCPosition = "cc.db last position" // p_lastAccessedPosition ÷ text length (UNVERIFIED)
	SrcSidecar    = "sidecar lpr"         // .azw3f lpr ÷ text length (fallback)
)

// ResolvePercent picks the book's percent from the first usable source.
// It returns 0 and "" when no source has a value. notes are log lines,
// including the sidecar cross-check.
func ResolvePercent(l *book.Local, meta *mobi.Meta, sc *sidecar.Position) (float64, string, []string) {
	var notes []string
	var textLen int64
	if meta != nil {
		textLen = meta.TextLength
	}
	fromPos := func(pos int64) float64 {
		if pos < 0 || textLen <= 0 {
			return 0
		}
		return min(100, float64(pos)/float64(textLen)*100)
	}

	scPct := 0.0
	if sc != nil && sc.LPR != "" {
		scPct = fromPos(sc.LPRPos)
		notes = append(notes, fmt.Sprintf("sidecar lpr %s fpr %s saved %s → %.2f%% (text length %d)",
			sc.LPR, sc.FPR, sc.Saved.UTC().Format("2006-01-02 15:04:05"), scPct, textLen))
	}

	if l.Percent > 0 {
		return l.Percent, SrcCCPercent, notes
	}
	if pos := lprNumber(l.LastPosition); pos >= 0 {
		if p := fromPos(pos); p > 0 {
			return p, SrcCCPosition, notes
		}
		notes = append(notes, fmt.Sprintf("cc.db last position %q: no text length", l.LastPosition))
	}
	if scPct > 0 {
		return scPct, SrcSidecar, notes
	}
	return 0, "", notes
}

// lprNumber reads "#1234" or "1234"; -1 if not a plain number.
func lprNumber(s string) int64 {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}
