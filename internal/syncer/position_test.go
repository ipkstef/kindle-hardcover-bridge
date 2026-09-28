package syncer

import (
	"testing"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/sidecar"
)

func TestResolvePercentOrder(t *testing.T) {
	meta := &mobi.Meta{TextLength: 933800}
	sc := &sidecar.Position{LPR: "68089", LPRPos: 68089, FPRPos: 68089}

	// 1. cc.db percent wins (device values from the sleep research).
	p, src, notes := ResolvePercent(&book.Local{Percent: 7.290831, LastPosition: "#1"}, meta, sc)
	if src != SrcCCPercent || p != 7.290831 || len(notes) != 1 {
		t.Fatalf("got %v %q %v", p, src, notes)
	}
	// 2. cc.db last position when percent is missing.
	p, src, _ = ResolvePercent(&book.Local{LastPosition: "#46690"}, meta, sc)
	if src != SrcCCPosition || p < 4.99 || p > 5.01 {
		t.Fatalf("got %v %q", p, src)
	}
	// 3. sidecar only when both cc.db sources fail.
	p, src, _ = ResolvePercent(&book.Local{LastPosition: "AmoC:12"}, meta, sc)
	if src != SrcSidecar || p < 7.29 || p > 7.30 {
		t.Fatalf("got %v %q", p, src)
	}
	// No text length (e.g. KFX): positions cannot give a percent.
	p, src, _ = ResolvePercent(&book.Local{}, nil, sc)
	if src != "" || p != 0 {
		t.Fatalf("got %v %q", p, src)
	}
}
