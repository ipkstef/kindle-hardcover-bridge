// Command hcbridge is the Kindle → Hardcover prototype.
//
//	hcbridge login    sign in with a device code (shown on the Kindle screen)
//	hcbridge sync     send the current book's progress once
//	hcbridge whoami   show the signed-in user
//	hcbridge logout   delete the saved token
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/certs"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/config"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/screen"
)

// clientID is set at build time: -ldflags "-X main.clientID=..."
// It is public by design (OAuth public client, no secret).
var clientID = ""

type app struct {
	oauth  *hardcover.OAuth
	store  *config.TokenStore
	db     *readers.Database
	screen *screen.Screen
	scope  string
}

func main() {
	fs := flag.NewFlagSet("hcbridge", flag.ExitOnError)
	cid := fs.String("client-id", clientID, "Hardcover OAuth client ID")
	state := fs.String("state", config.DefaultStateDir, "state directory (token)")
	dbPath := fs.String("db", config.DefaultCCDB, "path to cc.db")
	logPath := fs.String("log", config.DefaultLog, "log file (empty: stderr only)")
	scope := fs.String("scope", hardcover.DefaultScope, "OAuth scopes")
	row := fs.Int("row", 3, "first screen row for messages")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hcbridge login|sync|whoami|logout [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	_ = fs.Parse(os.Args[2:])

	setupLog(*logPath)
	a := &app{
		store:  config.NewTokenStore(*state),
		db:     &readers.Database{Path: *dbPath},
		screen: screen.New(*row),
		scope:  *scope,
	}
	a.oauth = hardcover.NewOAuth(httpClient(), strings.TrimSpace(*cid))

	ctx := context.Background()
	var err error
	switch cmd {
	case "login":
		err = a.login(ctx)
	case "sync":
		err = a.sync(ctx)
	case "whoami":
		err = a.whoami(ctx)
	case "logout":
		err = a.store.Delete()
		if err == nil {
			a.screen.Show("Signed out.")
		}
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Printf("%s: error: %v", cmd, err)
		msg := err.Error()
		if errors.Is(err, config.ErrNoToken) || errors.Is(err, hardcover.ErrUnauthorized) {
			msg = "Not signed in. Use 'Sign in' first."
		}
		a.screen.Show("Hardcover: ERROR", trim(msg, 46), "Details: hcbridge.log")
		os.Exit(1)
	}
}

func (a *app) login(ctx context.Context) error {
	if a.oauth.ClientID == "" {
		return errors.New("no client ID (client_id.txt missing)")
	}
	dc, err := a.oauth.RequestDeviceCode(ctx, a.scope)
	if err != nil {
		return err
	}
	log.Printf("login: device code issued, user code %s, expires in %ds", dc.UserCode, dc.ExpiresIn)
	a.screen.Show(
		"Hardcover sign in",
		"",
		"On your phone, open:",
		"  "+strings.TrimPrefix(dc.VerificationURI, "https://"),
		"Enter code:",
		"  "+dc.UserCode,
		"",
		fmt.Sprintf("Waiting (up to %d min)...", max(1, dc.ExpiresIn/60)),
	)
	tok, err := a.oauth.PollToken(ctx, dc)
	if err != nil {
		return err
	}
	if err := a.store.Save(tok); err != nil {
		return err
	}
	me, err := a.client().Me(ctx)
	if err != nil {
		return fmt.Errorf("signed in, but 'me' failed: %w", err)
	}
	log.Printf("login: OK as %s (id %d), scope %q", me.Username, me.ID, tok.Scope)
	a.screen.Show("Signed in as @"+me.Username+".", "", "", "", "", "", "", "")
	return nil
}

func (a *app) whoami(ctx context.Context) error {
	me, err := a.client().Me(ctx)
	if err != nil {
		return err
	}
	a.screen.Show("Signed in as @" + me.Username)
	return nil
}

func (a *app) sync(ctx context.Context) error {
	local, err := a.db.CurrentBook(ctx)
	if err != nil {
		return err
	}
	log.Printf("sync: local book %q by %v, %.2f%%", local.Title, local.Authors, local.Percent)

	c := a.client()
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	ubs, err := c.CurrentlyReading(ctx, me.ID)
	if err != nil {
		return err
	}
	cands := make([]book.Candidate, len(ubs))
	for i := range ubs {
		cands[i] = book.Candidate{Title: ubs[i].Book.Title, Authors: ubs[i].Authors()}
		log.Printf("sync: currently reading on Hardcover: %q by %v", cands[i].Title, cands[i].Authors)
	}
	i := book.Match(*local, cands)
	if i < 0 {
		a.screen.Show("Hardcover: no match (skipped)", trim(local.Title, 46),
			"Add it to 'Currently Reading'", "on Hardcover, then try again.")
		return nil
	}
	ub := &ubs[i]
	pages, editionID := ub.Pages()
	if pages <= 0 {
		return fmt.Errorf("no page count for %q on Hardcover", ub.Book.Title)
	}
	page := book.PercentToPage(local.Percent, pages)

	read := ub.CurrentRead()
	switch {
	case read != nil && read.ProgressPages != nil && *read.ProgressPages == page:
		log.Printf("sync: already at page %d, nothing sent", page)
	case read != nil && read.ProgressPages != nil && *read.ProgressPages > page:
		// Forward only: paging back (maps, notes) must not lower progress.
		// Restarting a book is handled separately (TODO, docs/open-questions.md).
		log.Printf("sync: Kindle page %d < Hardcover page %d, not sent (forward only)", page, *read.ProgressPages)
		a.screen.Show("Hardcover: not sent", trim(ub.Book.Title, 46),
			fmt.Sprintf("Kindle p%d is behind Hardcover p%d", page, *read.ProgressPages))
		return nil
	case read != nil:
		if read.EditionID != nil {
			editionID = read.EditionID
		}
		if _, err := c.UpdateReadProgress(ctx, read.ID, page, editionID, read.StartedAt); err != nil {
			return err
		}
		log.Printf("sync: updated read %d to page %d/%d", read.ID, page, pages)
	default:
		today := time.Now().Format("2006-01-02")
		r, err := c.InsertRead(ctx, ub.ID, page, editionID, today)
		if err != nil {
			return err
		}
		log.Printf("sync: new read %d at page %d/%d", r.ID, page, pages)
	}
	a.screen.Show("Hardcover: synced", trim(ub.Book.Title, 46),
		fmt.Sprintf("Page %d of %d (%.0f%%)", page, pages, local.Percent))
	return nil
}

// client returns a GraphQL client that refreshes the token when needed.
func (a *app) client() *hardcover.Client {
	return &hardcover.Client{
		HTTP:     a.oauth.HTTP,
		Endpoint: hardcover.GraphQLEndpoint,
		Token: func(ctx context.Context) (string, error) {
			tok, err := a.store.Load()
			if err != nil {
				return "", err
			}
			if tok.Expired(5*time.Minute) && tok.RefreshToken != "" && a.oauth.ClientID != "" {
				nt, err := a.oauth.Refresh(ctx, tok.RefreshToken)
				if err != nil {
					log.Printf("token refresh failed: %v", err)
					return "", hardcover.ErrUnauthorized
				}
				if nt.RefreshToken == "" {
					nt.RefreshToken = tok.RefreshToken
				}
				if err := a.store.Save(nt); err != nil {
					return "", err
				}
				log.Printf("token refreshed")
				tok = nt
			}
			return tok.AccessToken, nil
		},
	}
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: certs.Pool()},
		},
	}
}

func setupLog(path string) {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return // /mnt/us may be unmounted (USB); log to stderr only
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..." // eips has no "…" glyph (UNVERIFIED)
}
