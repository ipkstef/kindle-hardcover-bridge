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

// FinishedPercent: above this, the book counts as finished (user decision).
// Fallback: the Kindle marks the book read (p_readState = 2).
const FinishedPercent = 99.0

// IsFinished applies the finish rule.
func IsFinished(pct float64, readState int) bool {
	return pct > FinishedPercent || readState == 2
}

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
	Kind     Kind
	Title    string // Hardcover title
	Page     int
	Pages    int
	Reason   string // for Skipped
	Added    string // "added to Currently Reading", "moved from Want to Read", or ""
	Finished bool   // the book was marked Read on Hardcover
}

// Syncer holds the Hardcover client, the match cache and a logger.
type Syncer struct {
	C     *hardcover.Client
	Cache *BookCache // optional
	Logf  func(format string, a ...any)
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
	res, err := s.Resolve(ctx, local)
	if err != nil {
		return nil, nil, err
	}
	me, err := s.C.Me(ctx) // cached
	if err != nil {
		return nil, nil, err
	}
	ub, err := s.C.UserBookForBook(ctx, me.ID, res.BookID)
	if err != nil {
		return nil, nil, err
	}
	shelf := "not on your shelves"
	if ub != nil {
		shelf = StatusName(ub.StatusID)
	}
	s.logf("identify: %q → book %d via %s, %s", local.Title, res.BookID, res.Method, shelf)
	return res, ub, nil
}

// Resolve finds the Hardcover book: from the match cache, else with the
// match waterfall (then cached). No shelf lookup.
func (s *Syncer) Resolve(ctx context.Context, local *book.Local) (*match.Result, error) {
	if s.Cache != nil && local.Key != "" {
		if m, ok := s.Cache.Get(local.Key); ok {
			return &match.Result{BookID: m.BookID, EditionID: m.EditionID, Pages: m.Pages,
				Title: m.Title, Method: "cache (" + m.Method + ")"}, nil
		}
	}
	var meta *mobi.Meta
	if !strings.Contains(local.MimeType, "kfx") {
		m, err := mobi.ReadFile(local.Path)
		if err != nil {
			s.logf("identify: book file not read: %v", err)
		}
		meta = m
	}
	id := book.BuildIdentity(*local, meta)
	s.logf("identify: local %q by %v, year %d", local.Title, local.Authors, id.Year)
	for _, x := range id.IDs {
		s.logf("identify: id %s %s from %s (dedicated %v)", x.Kind, x.Value, x.Source, x.Dedicated)
	}
	lib := func(ctx context.Context) ([]hardcover.UserBook, error) {
		me, err := s.C.Me(ctx)
		if err != nil {
			return nil, err
		}
		return s.C.Library(ctx, me.ID)
	}
	res, steps, err := match.Resolve(ctx, s.C, id, lib)
	for _, st := range steps {
		s.logf("identify: %s", st)
	}
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, ErrNotFound
	}
	if s.Cache != nil && local.Key != "" {
		if err := s.Cache.Put(local.Key, BookMap{BookID: res.BookID, EditionID: res.EditionID,
			Pages: res.Pages, Title: res.Title, Method: res.Method}); err != nil {
			s.logf("identify: cache: %v", err)
		}
	}
	return res, nil
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
	finished := IsFinished(pct, local.ReadState)
	if pct <= 0 && !finished {
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
	if finished {
		out, _, err := s.finish(ctx, local, res, ub)
		return out, err
	}

	added := ""
	switch {
	case ub == nil:
		nub, err := s.addUserBook(ctx, res, hardcover.StatusReading)
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
	// After an add or status change, the returned entry already holds the
	// read Hardcover created (seen 2026-09-28), so no extra lookup is needed.

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

// addUserBook puts the book on the user's shelf. Edition: the one the ID
// pointed to (same ebook), else the default ebook/physical edition. Privacy:
// the account default, so the user controls visibility on Hardcover
// (user decision).
func (s *Syncer) addUserBook(ctx context.Context, res *match.Result, status int) (*hardcover.UserBook, error) {
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
		return nil, err
	}
	privacy := 1 // public, as the Hardcover KOReader plugin does
	if me.PrivacyID != nil {
		privacy = *me.PrivacyID
	}
	return s.C.InsertUserBook(ctx, res.BookID, status, editionID, privacy)
}

// finish marks the book Read. Order (hardcover-features.md): finish the open
// read (last page + finished_at), then set status Read, so Hardcover does not
// add a second, empty finished read.
func (s *Syncer) finish(ctx context.Context, local *book.Local, res *match.Result, ub *hardcover.UserBook) (Outcome, *hardcover.UserBook, error) {
	today := time.Now().Format("2006-01-02")
	out := Outcome{Kind: Sent, Title: res.Title, Finished: true}
	switch {
	case ub != nil && ub.StatusID == hardcover.StatusRead:
		s.logf("sync: already Read on Hardcover")
		return Outcome{Kind: Unchanged, Title: res.Title, Finished: true}, ub, nil
	case ub != nil && (ub.StatusID == hardcover.StatusDNF || ub.StatusID == hardcover.StatusIgnored):
		s.logf("sync: not sent: shelf is %s", StatusName(ub.StatusID))
		return Outcome{Kind: Skipped, Title: res.Title, Reason: "shelf is " + StatusName(ub.StatusID)}, ub, nil
	case ub == nil:
		// Not on the shelves: add as Currently Reading first (creates a read),
		// then finish it below like any other book.
		nub, err := s.addUserBook(ctx, res, hardcover.StatusReading)
		if err != nil {
			return Outcome{}, nil, err
		}
		s.logf("sync: auto-added book %d (user_book %d) to finish it", res.BookID, nub.ID)
		ub, out.Added = nub, "added"
	}
	// Already finished today (e.g. Hardcover moved the status back, or a
	// retry): only set the status, never add a second finished read.
	for _, r := range ub.Reads {
		if r.FinishedAt != nil && *r.FinishedAt == today {
			if ub.StatusID != hardcover.StatusRead {
				nub, err := s.C.SetStatus(ctx, ub.ID, hardcover.StatusRead)
				if err != nil {
					return Outcome{}, nil, err
				}
				s.logf("sync: read %d already finished today; status set to Read", r.ID)
				return out, nub, nil
			}
			return Outcome{Kind: Unchanged, Title: res.Title, Finished: true}, ub, nil
		}
	}
	pages, editionID := ub.Pages()
	if pages <= 0 && res.Pages > 0 {
		pages, editionID = res.Pages, res.EditionID
	}
	read := ub.CurrentRead()
	if read == nil {
		r, err := s.C.InsertRead(ctx, ub.ID, pages, editionID, today)
		if err != nil {
			return Outcome{}, nil, err
		}
		read = r
	}
	if read.EditionID != nil {
		editionID = read.EditionID
	}
	if _, err := s.C.FinishRead(ctx, read.ID, pages, editionID, read.StartedAt, today); err != nil {
		return Outcome{}, nil, err
	}
	nub, err := s.C.SetStatus(ctx, ub.ID, hardcover.StatusRead)
	if err != nil {
		return Outcome{}, nil, err
	}
	s.logf("sync: finished read %d (page %d/%d, %s), status Read (Kindle %.2f%%, read state %d)",
		read.ID, pages, pages, today, local.Percent, local.ReadState)
	out.Page, out.Pages = pages, pages
	if ub.Book.Title != "" {
		out.Title = ub.Book.Title
	}
	return out, nub, nil
}
