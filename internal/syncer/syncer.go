// Package syncer sends one local book's progress to Hardcover.
// Used by the manual menu commands and by the daemon.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/match"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/sidecar"
)

// FinishedPercent: at or above this, the book counts as finished. Finishing
// (status Read) is not built yet, so such books are not sent.
const FinishedPercent = 99.0

// ErrNotFound means the waterfall found no confident match.
var ErrNotFound = errors.New("book not found on Hardcover")

// Kind is the result of one sync.
type Kind int

const (
	Sent      Kind = iota // progress written
	Unchanged             // Hardcover already has this page
	Skipped               // by a rule (backward, finished, shelf, ...)
)

// Outcome describes what Sync did.
type Outcome struct {
	Kind   Kind
	Title  string // Hardcover title
	Page   int
	Pages  int
	Reason string // for Skipped
	Added  string // "added to Currently Reading", "moved from Want to Read", or ""
}

// Syncer holds the Hardcover client and a logger.
type Syncer struct {
	C    *hardcover.Client
	Logf func(format string, a ...any)
}

func (s *Syncer) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

// StatusName is a readable shelf name.
func StatusName(id int) string {
	switch id {
	case hardcover.StatusWantToRead:
		return "Want to Read"
	case hardcover.StatusReading:
		return "Currently Reading"
	case hardcover.StatusRead:
		return "Read"
	case hardcover.StatusDNF:
		return "Did Not Finish"
	}
	return fmt.Sprintf("status %d", id)
}

// Identify finds the book on Hardcover with the match waterfall. ub is the
// user's shelf entry, or nil if the book is not on the user's shelves.
// It returns ErrNotFound when nothing matches.
func (s *Syncer) Identify(ctx context.Context, local *book.Local) (*match.Result, *hardcover.UserBook, error) {
	var meta *mobi.Meta
	if !strings.Contains(local.MimeType, "kfx") {
		m, err := mobi.ReadFile(local.Path)
		if err != nil {
			s.logf("identify: book file not read: %v", err)
		}
		meta = m
	}
	id := book.BuildIdentity(*local, meta)
	s.logf("identify: local %q by %v, %.2f%%, year %d", local.Title, local.Authors, local.Percent, id.Year)
	for _, x := range id.IDs {
		s.logf("identify: id %s %s from %s (dedicated %v)", x.Kind, x.Value, x.Source, x.Dedicated)
	}

	me, err := s.C.Me(ctx)
	if err != nil {
		return nil, nil, err
	}
	lib, err := s.C.Library(ctx, me.ID)
	if errors.Is(err, hardcover.ErrUnauthorized) {
		return nil, nil, err
	}
	if err != nil {
		// Not fatal: IDs and search still work without the library step.
		s.logf("identify: library query failed: %v", err)
	}
	res, steps, err := match.Resolve(ctx, s.C, id, lib)
	for _, st := range steps {
		s.logf("identify: %s", st)
	}
	if err != nil {
		return nil, nil, err
	}
	if res == nil {
		return nil, nil, ErrNotFound
	}
	var ub *hardcover.UserBook
	for i := range lib {
		if lib[i].BookID == res.BookID {
			ub = &lib[i]
			break
		}
	}
	shelf := "not on your shelves"
	if ub != nil {
		shelf = StatusName(ub.StatusID)
	}
	s.logf("identify: result book %d %q via %s (%s), %s", res.BookID, res.Title, res.Method, res.Via, shelf)
	return res, ub, nil
}

// Sync sends the book's progress. Rules:
//   - forward only; percent 0 and ≥ FinishedPercent are not sent;
//   - not on shelves → add to Currently Reading; Want to Read → move there;
//   - Read / DNF → not sent (re-read rule not built yet).
//
// A returned error means "try again later" (network, auth, API error).
func (s *Syncer) Sync(ctx context.Context, local *book.Local) (Outcome, error) {
	skip := func(title, why string) (Outcome, error) {
		s.logf("sync: not sent: %s", why)
		return Outcome{Kind: Skipped, Title: title, Reason: why}, nil
	}
	// Position: cc.db percent, then cc.db last position, then sidecar lpr.
	var meta *mobi.Meta
	if !strings.Contains(local.MimeType, "kfx") {
		meta, _ = mobi.ReadFile(local.Path)
	}
	var sc *sidecar.Position
	if p, err := sidecar.ForBook(local.Path); err == nil {
		sc = &p
	}
	pct, src, notes := ResolvePercent(local, meta, sc)
	for _, n := range notes {
		s.logf("position: %s", n)
	}
	if pct <= 0 {
		return skip(local.Title, "book not started on the Kindle")
	}
	s.logf("position: %.2f%% from %s", pct, src)
	if pct != local.Percent {
		cp := *local
		cp.Percent = pct
		local = &cp
	}
	res, ub, err := s.Identify(ctx, local)
	if errors.Is(err, ErrNotFound) {
		return skip(local.Title, "book not found on Hardcover")
	}
	if err != nil {
		return Outcome{}, err
	}
	if local.Percent >= FinishedPercent {
		return skip(res.Title, "finished book: not built yet")
	}

	added := ""
	switch {
	case ub == nil:
		// Prefer the edition the ID pointed to (same ebook), else the
		// book's default ebook/physical edition.
		editionID := res.EditionID
		if editionID == nil || res.Pages <= 0 {
			if de, err := s.C.DefaultEdition(ctx, res.BookID); err != nil {
				s.logf("sync: default edition: %v", err)
			} else if de != nil {
				editionID = &de.ID
			}
		}
		me, err := s.C.Me(ctx)
		if err != nil {
			return Outcome{}, err
		}
		privacy := 1 // public, as the Hardcover KOReader plugin does
		if me.PrivacyID != nil {
			privacy = *me.PrivacyID
		}
		nub, err := s.C.InsertUserBook(ctx, res.BookID, hardcover.StatusReading, editionID, privacy)
		if err != nil {
			return Outcome{}, err
		}
		s.logf("sync: auto-added book %d to Currently Reading (user_book %d)", res.BookID, nub.ID)
		ub, added = nub, "added to Currently Reading"
	case ub.StatusID == hardcover.StatusWantToRead:
		nub, err := s.C.SetStatus(ctx, ub.ID, hardcover.StatusReading)
		if err != nil {
			return Outcome{}, err
		}
		s.logf("sync: moved user_book %d from Want to Read to Currently Reading", ub.ID)
		ub, added = nub, "moved from Want to Read"
	case ub.StatusID != hardcover.StatusReading:
		return skip(res.Title, "shelf is "+StatusName(ub.StatusID)+" (re-read: not built yet)")
	}
	if added != "" {
		// Hardcover creates a read on status change (seen 2026-09-28).
		// Re-read the entry so we update that read and do not add a second.
		if fresh, err := s.C.UserBookByID(ctx, ub.ID); err != nil {
			s.logf("sync: re-read user_book %d: %v", ub.ID, err)
		} else {
			ub = fresh
		}
	}

	pages, editionID := ub.Pages()
	if pages <= 0 && res.Pages > 0 {
		pages, editionID = res.Pages, res.EditionID
	}
	if pages <= 0 {
		return skip(res.Title, "no page count on Hardcover")
	}
	page := book.PercentToPage(local.Percent, pages)
	out := Outcome{Title: ub.Book.Title, Page: page, Pages: pages, Added: added}
	if out.Title == "" {
		out.Title = res.Title
	}

	read := ub.CurrentRead()
	switch {
	case read != nil && read.ProgressPages != nil && *read.ProgressPages == page:
		s.logf("sync: already at page %d, nothing sent", page)
		out.Kind = Unchanged
	case read != nil && read.ProgressPages != nil && *read.ProgressPages > page:
		// Forward only: paging back (maps, notes) must not lower progress.
		out.Kind, out.Reason = Skipped, fmt.Sprintf("Kindle p%d is behind Hardcover p%d", page, *read.ProgressPages)
		s.logf("sync: not sent: %s (forward only)", out.Reason)
	case read != nil:
		if read.EditionID != nil {
			editionID = read.EditionID
		}
		if _, err := s.C.UpdateReadProgress(ctx, read.ID, page, editionID, read.StartedAt); err != nil {
			return Outcome{}, err
		}
		s.logf("sync: updated read %d to page %d/%d", read.ID, page, pages)
		out.Kind = Sent
	default:
		today := time.Now().Format("2006-01-02")
		r, err := s.C.InsertRead(ctx, ub.ID, page, editionID, today)
		if err != nil {
			return Outcome{}, err
		}
		s.logf("sync: new read %d at page %d/%d", r.ID, page, pages)
		out.Kind = Sent
	}
	return out, nil
}
