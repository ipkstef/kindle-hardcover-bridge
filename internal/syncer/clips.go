package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/atomicfile"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/clippings"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/mobi"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
)

// Books is the part of the cc.db reader the clip sync needs.
type Books interface {
	BooksByTitle(ctx context.Context, title string) ([]*book.Local, error)
}

// ClipState is saved between runs.
type ClipState struct {
	Baseline bool           `json:"baseline"` // existing clippings were recorded
	Sent     map[string]int `json:"sent"`     // clip ID → journal ID (0 = baseline, not sent)
	Size     int64          `json:"size"`
	ModTime  time.Time      `json:"mod_time"`
}

// ClipSync sends Kindle highlights as private quotes and notes as private
// notes to the Hardcover reading journal (user decision 2026-09-28).
type ClipSync struct {
	S         *Syncer
	Books     Books
	Path      string       // My Clippings.txt
	StatePath string       // JSON state (used when DB is nil; imported once)
	DB        *store.Store // optional

	sent *store.Table[int]
}

// clipMeta is the small part of ClipState, read before the sent list.
type clipMeta struct {
	Baseline bool      `json:"baseline"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
}

const kvClips = "clips.meta"

// Run sends new clippings. With all=true, clippings recorded as baseline are
// sent too (import of old highlights). It does nothing if the file did not
// change since the last complete run.
func (c *ClipSync) Run(ctx context.Context, all bool) (sent int, err error) {
	fi, err := os.Stat(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err // e.g. /mnt/us not mounted (USB mode): retry later
	}
	// Cheap check first: the sent list (can be thousands of rows) is read
	// only when My Clippings.txt changed.
	if m := c.meta(); !all && m.Baseline && fi.Size() == m.Size && fi.ModTime().Equal(m.ModTime) {
		return 0, nil
	}
	st := c.load()
	f, err := os.Open(c.Path)
	if err != nil {
		return 0, err
	}
	clips, err := clippings.Parse(f)
	f.Close()
	if err != nil {
		return 0, err
	}

	if pruned := pruneSent(st, clips); pruned > 0 {
		c.S.logf("clips: %d entries of deleted clippings removed from the sent list", pruned)
	}
	if migrateIDs(st, clips) {
		if err := c.save(st); err != nil {
			return 0, err
		}
	}

	if !st.Baseline && !all {
		for _, cl := range clips {
			st.Sent[cl.ID()] = 0
		}
		st.Baseline, st.Size, st.ModTime = true, fi.Size(), fi.ModTime()
		c.S.logf("clips: first run, %d existing clippings recorded, not sent (use import to send them)", len(clips))
		return 0, c.save(st)
	}

	type key struct{ title, author string }
	groups := map[key][]clippings.Clip{}
	var order []key
	pairs := pairNotes(clips)
	for _, cl := range clips {
		if h, ok := pairs.highlightOf[cl.ID()]; ok && cl.Kind == clippings.Note {
			// The note carries its highlight; mark the highlight as merged.
			cl.Text = noteWithHighlight(h.Text, cl.Text)
		}
		if _, merged := pairs.merged[cl.ID()]; merged {
			if _, done := st.Sent[cl.ID()]; !done {
				st.Sent[cl.ID()] = -2 // sent inside its note
			}
			continue
		}
		if cl.Kind != clippings.Highlight && cl.Kind != clippings.Note {
			continue
		}
		if jid, done := st.Sent[cl.ID()]; done && (jid != 0 || !all) {
			continue
		}
		k := key{cl.Title, cl.Author}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], cl)
	}

	complete := true
	for _, k := range order {
		n, err := c.sendBook(ctx, k.title, k.author, groups[k], st)
		sent += n
		if err != nil {
			_ = c.save(st) // keep what was sent (e.g. Wi-Fi lost mid-book)
			return sent, err
		}
		if n > 0 {
			if err := c.save(st); err != nil {
				return sent, err
			}
		}
		if n < len(groups[k]) {
			complete = false
		}
	}
	st.Baseline = true
	if complete {
		st.Size, st.ModTime = fi.Size(), fi.ModTime()
	}
	return sent, c.save(st)
}

// sendBook sends one book's clippings. Clips of books that cannot be found
// stay unsent and are tried again when the file changes.
func (c *ClipSync) sendBook(ctx context.Context, title, author string, clips []clippings.Clip, st *ClipState) (int, error) {
	local := c.findLocal(ctx, title, author)
	if local == nil {
		c.S.logf("clips: %q: book not found in cc.db, %d clippings kept for later", title, len(clips))
		return 0, nil
	}
	res, ub, err := c.S.Identify(ctx, local)
	if errors.Is(err, ErrNotFound) {
		c.S.logf("clips: %q: not found on Hardcover, %d clippings kept for later", title, len(clips))
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	pages, editionID := res.Pages, res.EditionID
	if ub != nil {
		if p, e := ub.Pages(); p > 0 {
			pages, editionID = p, e
		}
	}
	var textLen int64
	if !strings.Contains(local.MimeType, "kfx") {
		if m, err := mobi.ReadFile(local.Path); err == nil {
			textLen = m.TextLength
		}
	}

	n := 0
	for _, cl := range clips {
		if strings.TrimSpace(cl.Text) == "" {
			st.Sent[cl.ID()] = -1 // nothing to send
			n++
			continue
		}
		e := hardcover.JournalEntry{
			BookID: res.BookID, EditionID: editionID, Entry: cl.Text,
			PrivacyID: hardcover.PrivacyPrivate, Event: "quote",
		}
		if cl.Kind == clippings.Note {
			e.Event = "note"
		}
		if !cl.Added.IsZero() {
			e.ActionAt = cl.Added.Format("2006-01-02")
		}
		if page := locationToPage(cl.LocStart, textLen, pages); page > 0 {
			e.Metadata = map[string]any{"position": map[string]any{"type": "pages", "value": page, "possible": pages}}
		}
		jid, err := c.S.C.InsertJournal(ctx, e)
		if err != nil {
			return n, err
		}
		st.Sent[cl.ID()] = jid
		n++
		c.S.logf("clips: %q: %s at location %d → private journal %d", res.Title, e.Event, cl.LocStart, jid)
		// Save every saveEvery clips, not after each one (an import of
		// thousands would rewrite a growing file thousands of times). A
		// power loss can then send at most saveEvery-1 clips twice.
		if n%saveEvery == 0 {
			if err := c.save(st); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// pruneSent removes sent-list entries whose clipping is no longer in
// My Clippings.txt (deleted by the user or the Kindle). They can never be
// sent again, so the list does not grow forever. An empty parse result is
// ignored (a file being rewritten must not clear the list). Entries of the
// old ID format are kept: migrateIDs moves them.
func pruneSent(st *ClipState, clips []clippings.Clip) int {
	if len(clips) == 0 {
		return 0
	}
	keep := make(map[string]bool, 2*len(clips))
	for _, cl := range clips {
		keep[cl.ID()] = true
		keep[cl.LegacyID()] = true
	}
	n := 0
	for id := range st.Sent {
		if !keep[id] {
			delete(st.Sent, id)
			n++
		}
	}
	return n
}

// migrateIDs moves entries of the sent list from the old, time-zone-based
// clip ID to the new one. It reports if anything changed.
func migrateIDs(st *ClipState, clips []clippings.Clip) bool {
	changed := false
	for _, cl := range clips {
		old, id := cl.LegacyID(), cl.ID()
		if old == id {
			continue
		}
		if jid, ok := st.Sent[old]; ok {
			if _, done := st.Sent[id]; !done {
				st.Sent[id] = jid
			}
			delete(st.Sent, old)
			changed = true
		}
	}
	return changed
}

const saveEvery = 20

func (c *ClipSync) findLocal(ctx context.Context, title, author string) *book.Local {
	books, err := c.Books.BooksByTitle(ctx, title)
	if err != nil || len(books) == 0 {
		return nil
	}
	if author != "" {
		for _, b := range books {
			for _, a := range b.Authors {
				if book.NormName(a) == book.NormName(author) {
					return b
				}
			}
		}
	}
	// No author match: use the book only when there is no doubt (never send
	// notes to another book with the same title).
	if len(books) == 1 {
		return books[0]
	}
	return nil
}

// locationToPage maps a Kindle location to a page of the Hardcover edition.
// MOBI/AZW3: location ≈ text position / 150 (common rule, UNVERIFIED on the
// device). Returns 0 when it cannot be computed.
func locationToPage(loc int, textLen int64, pages int) int {
	if loc <= 0 || textLen <= 0 || pages <= 0 {
		return 0
	}
	pct := float64(loc) * 150 / float64(textLen) * 100
	return book.PercentToPage(min(pct, 100), pages)
}

func (c *ClipSync) meta() clipMeta {
	if c.DB != nil {
		c.importJSON()
		var m clipMeta
		c.DB.GetJSON(kvClips, &m)
		return m
	}
	st := c.load()
	return clipMeta{st.Baseline, st.Size, st.ModTime}
}

func (c *ClipSync) importJSON() {
	if c.sent == nil {
		c.sent = store.NewTable[int](c.DB, store.TClips)
	}
	_ = c.DB.ImportJSON("clips.json", c.StatePath, func(b []byte) error {
		var st ClipState
		if json.Unmarshal(b, &st) != nil {
			return nil
		}
		if err := c.sent.Save(st.Sent); err != nil {
			return err
		}
		return c.DB.SetJSON(kvClips, clipMeta{st.Baseline, st.Size, st.ModTime})
	})
}

func (c *ClipSync) load() *ClipState {
	st := &ClipState{}
	if c.DB != nil {
		c.importJSON()
		m := c.meta()
		st.Baseline, st.Size, st.ModTime = m.Baseline, m.Size, m.ModTime
		st.Sent, _ = c.sent.Load()
	} else if b, err := os.ReadFile(c.StatePath); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Sent == nil {
		st.Sent = map[string]int{}
	}
	return st
}

// save writes the state. With the DB only new or removed clips are
// written, with the meta, in one transaction.
func (c *ClipSync) save(st *ClipState) error {
	if c.DB == nil {
		return atomicfile.GuardedJSON(c.StatePath, st, 0o600)
	}
	ops, commit, err := c.sent.Diff(st.Sent)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(clipMeta{st.Baseline, st.Size, st.ModTime})
	if err := c.DB.Apply(append(ops, store.SetOp(kvClips, string(b)))...); err != nil {
		return err
	}
	commit()
	return nil
}

// Note/highlight pairing. On the Kindle, a note on a highlight is saved as
// two clippings: the note (at the highlight's last location) and the
// highlight, with the same time (seen on FW 5.17.1: "Note Location 648" and
// "Highlight Location 648-648", same "Added on").
type notePairs struct {
	highlightOf map[string]clippings.Clip // note ID → its highlight
	merged      map[string]struct{}       // highlight IDs sent inside a note
}

// pairWindow: a note and its highlight are saved within this time.
const pairWindow = 2 * time.Minute

func pairNotes(clips []clippings.Clip) notePairs {
	p := notePairs{highlightOf: map[string]clippings.Clip{}, merged: map[string]struct{}{}}
	for _, n := range clips {
		if n.Kind != clippings.Note || n.LocStart == 0 {
			continue
		}
		best, found := clippings.Clip{}, false
		for _, h := range clips {
			if h.Kind != clippings.Highlight || h.Title != n.Title || h.Author != n.Author {
				continue
			}
			if n.LocStart < h.LocStart || n.LocStart > h.LocEnd {
				continue
			}
			if !n.Added.IsZero() && !h.Added.IsZero() {
				d := n.Added.Sub(h.Added)
				if d < -pairWindow || d > pairWindow {
					continue
				}
			}
			if _, used := p.merged[h.ID()]; used {
				continue
			}
			best, found = h, true
		}
		if found {
			p.highlightOf[n.ID()] = best
			p.merged[best.ID()] = struct{}{}
		}
	}
	return p
}

// noteWithHighlight is the journal text for a note on a highlight.
func noteWithHighlight(highlight, note string) string {
	return "Highlight:\n“" + strings.TrimSpace(highlight) + "”\n\nNote:\n" + strings.TrimSpace(note)
}
