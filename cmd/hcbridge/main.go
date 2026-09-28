// Command hcbridge syncs Kindle reading progress to Hardcover.
//
//	hcbridge login    sign in with a device code (shown on the Kindle screen)
//	hcbridge daemon   run in the background, sync on events
//	hcbridge stop     stop the daemon
//	hcbridge status   show daemon state on screen
//	hcbridge sync     send the current book's progress once
//	hcbridge identify find the current book on Hardcover (no writes)
//	hcbridge clips    send new highlights/notes (private journal entries)
//	hcbridge clipsall send all highlights/notes, also old ones
//	hcbridge savelog  copy the log to the USB drive
//	hcbridge dialogprobe collect dialog files and test pillow boxes (research)
//	hcbridge whoami   show the signed-in user
//	hcbridge logout   delete the saved token
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/certs"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/clippings"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/config"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/metrics"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/readers"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/screen"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/store"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/syncer"
)

// clientID is set at build time: -ldflags "-X main.clientID=..."
// It is public by design (OAuth public client, no secret).
var clientID = ""

// syncShelves: send the Goodreads shelf choice from the end-of-book dialog
// as a Hardcover status. Off in the first release (user decision
// 2026-09-28: default logic only); built and tested, ready to turn on.
const syncShelves = false

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

type app struct {
	oauth    *hardcover.OAuth
	store    *config.TokenStore
	db       *readers.Database
	screen   *screen.Screen
	scope    string
	stateDir string
	logPath  string

	hc    *hardcover.Client // one per process: rate limiter + cached "me"
	sy    *syncer.Syncer
	tokMu sync.Mutex

	sdb       *store.Store
	sdbFailed bool
}

func main() {
	fs := flag.NewFlagSet("hcbridge", flag.ExitOnError)
	cid := fs.String("client-id", clientID, "Hardcover OAuth client ID")
	state := fs.String("state", config.DefaultStateDir, "state directory (token, daemon state)")
	dbPath := fs.String("db", config.DefaultCCDB, "path to cc.db")
	logPath := fs.String("log", config.DefaultLog, "log file (empty: stderr only)")
	scope := fs.String("scope", hardcover.DefaultScope, "OAuth scopes")
	row := fs.Int("row", 3, "first screen row for messages")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hcbridge login|daemon|stop|status|sync|identify|clips|clipsall|savelog|dialogprobe|whoami|logout [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	_ = fs.Parse(os.Args[2:])

	setupLog(*logPath)
	a := &app{
		store:    config.NewTokenStore(*state),
		db:       &readers.Database{Path: *dbPath},
		screen:   screen.New(*row),
		scope:    *scope,
		stateDir: *state,
		logPath:  *logPath,
	}
	a.oauth = hardcover.NewOAuth(httpClient(), strings.TrimSpace(*cid))

	ctx := context.Background()
	var err error
	switch cmd {
	case "login":
		err = a.login(ctx)
	case "daemon":
		err = a.daemon(ctx)
	case "stop":
		err = a.stop()
	case "status":
		err = a.status()
	case "identify":
		err = a.identify(ctx)
	case "sync":
		err = a.syncNow(ctx)
	case "clips", "clipsall":
		err = a.clipsNow(ctx, cmd == "clipsall")
	case "savelog":
		err = a.saveLog()
	case "dialogprobe":
		err = a.dialogProbe(ctx)
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
	if a.sdb != nil {
		a.sdb.Close()
	}
	if err != nil {
		log.Printf("%s: error: %v", cmd, err)
		msg := err.Error()
		if errors.Is(err, config.ErrNoToken) || errors.Is(err, hardcover.ErrUnauthorized) {
			msg = "Not signed in. Use 'Sign in' first."
		}
		if cmd != "daemon" { // the daemon never draws on the screen
			a.screen.Show("Hardcover: ERROR", trim(msg, 46), "Details: Save log to USB")
		}
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
	msg := "Background sync is ON."
	if err := a.startDaemon(); err != nil {
		log.Printf("login: start daemon: %v", err)
		msg = "Tap 'Start background sync'."
	}
	a.screen.Show("Signed in as @"+me.Username+".", msg, "",
		"Tip: unlink Goodreads on the Kindle",
		"(Settings > Your Account) to avoid",
		"Goodreads error boxes. Ratings still", "go to Hardcover.", "")
	return nil
}

// startDaemon starts "hcbridge daemon" as its own background process (new
// session, output to the log), like the menu's "Start background sync".
func (a *app) startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{"daemon"}, os.Args[2:]...) // same flags (client ID, paths)
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (a *app) whoami(ctx context.Context) error {
	me, err := a.client().Me(ctx)
	if err != nil {
		return err
	}
	a.screen.Show("Signed in as @" + me.Username)
	return nil
}

func (a *app) syncer() *syncer.Syncer {
	if a.sy == nil {
		a.sy = &syncer.Syncer{C: a.client(), Logf: log.Printf,
			Cache: &syncer.BookCache{Path: filepath.Join(a.stateDir, "bookmap.json"), DB: a.stateDB()}}
	}
	return a.sy
}

func (a *app) identify(ctx context.Context) error {
	local, err := a.db.CurrentBook(ctx)
	if err != nil {
		return err
	}
	res, ub, err := a.syncer().Identify(ctx, local)
	if errors.Is(err, syncer.ErrNotFound) {
		a.screen.Show("Hardcover: book not found", trim(local.Title, 46), "Details: Save log to USB")
		return nil
	}
	if err != nil {
		return err
	}
	shelf := "not on your shelves"
	if ub != nil {
		shelf = syncer.StatusName(ub.StatusID)
	}
	a.screen.Show("Hardcover: found", trim(res.Title, 46), "via "+res.Method, "Shelf: "+shelf)
	return nil
}

func (a *app) syncNow(ctx context.Context) error {
	local, err := a.db.CurrentBook(ctx)
	if err != nil {
		return err
	}
	out, err := a.syncer().Sync(ctx, local)
	if err != nil {
		return err
	}
	switch out.Kind {
	case syncer.Skipped:
		a.screen.Show("Hardcover: not sent", trim(out.Title, 46), trim(out.Reason, 46))
	default:
		lines := []string{"Hardcover: synced", trim(out.Title, 46),
			fmt.Sprintf("Page %d of %d (%.0f%%)", out.Page, out.Pages, local.Percent)}
		if out.Finished {
			lines[2] = "Finished: status Read"
		}
		if out.Added != "" {
			lines = append(lines, out.Added)
		}
		a.screen.Show(lines...)
	}
	return nil
}

// stateDB opens the state database once (/var/local/hcbridge/hcbridge.db).
// If it cannot be opened, the JSON files are used (capability model: a
// missing backend never stops the daemon).
func (a *app) stateDB() *store.Store {
	if a.sdb == nil && !a.sdbFailed {
		s, err := store.Open(filepath.Join(a.stateDir, "hcbridge.db"))
		if err != nil {
			log.Printf("state db: %v (using JSON files)", err)
			a.sdbFailed = true
			return nil
		}
		a.sdb = s
	}
	return a.sdb
}

func (a *app) clipSync() *syncer.ClipSync {
	return &syncer.ClipSync{S: a.syncer(), Books: a.db, Path: clippings.DefaultPath,
		StatePath: filepath.Join(a.stateDir, "clips.json"), DB: a.stateDB()}
}

func (a *app) rateSync() *syncer.RateSync {
	return &syncer.RateSync{S: a.syncer(), Books: a.db, Path: metrics.DefaultPath, SyncShelves: syncShelves,
		StatePath: filepath.Join(a.stateDir, "ratings.json"), DB: a.stateDB()}
}

func (a *app) clipsNow(ctx context.Context, all bool) error {
	a.screen.Show("Hardcover: sending highlights/notes...")
	n, err := a.clipSync().Run(ctx, all)
	if err != nil {
		return err
	}
	a.screen.Show(fmt.Sprintf("Hardcover: %d highlights/notes sent", n), "(private journal entries)")
	return nil
}

// saveLog copies the log (and the rotated part) to the USB drive.
func (a *app) saveLog() error {
	var buf []byte
	for _, p := range []string{a.logPath + ".1", a.logPath} {
		if b, err := os.ReadFile(p); err == nil {
			buf = append(buf, b...)
		}
	}
	if err := os.WriteFile(config.USBLog, buf, 0o644); err != nil {
		return err
	}
	a.screen.Show("Log saved: "+filepath.Base(config.USBLog), "Connect USB to copy it.")
	return nil
}

// client returns the GraphQL client (one per process). It refreshes the
// token when needed.
func (a *app) client() *hardcover.Client {
	if a.hc == nil {
		a.hc = a.newClient()
	}
	return a.hc
}

func (a *app) newClient() *hardcover.Client {
	return &hardcover.Client{
		HTTP:     a.oauth.HTTP,
		Endpoint: hardcover.GraphQLEndpoint,
		Token:    a.token,
	}
}

// token returns a valid access token and refreshes it when it expires soon.
// One refresh at a time: a mutex in this process and a file lock across
// processes, then the token is read again (another caller may have
// refreshed it already; the old refresh token may no longer work).
func (a *app) token(ctx context.Context) (string, error) {
	a.tokMu.Lock()
	defer a.tokMu.Unlock()
	tok, err := a.store.Load()
	if err != nil {
		return "", err
	}
	if !a.needRefresh(tok) {
		return tok.AccessToken, nil
	}
	unlock, err := a.store.Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if tok, err = a.store.Load(); err != nil {
		return "", err
	}
	if !a.needRefresh(tok) {
		return tok.AccessToken, nil
	}
	nt, err := a.oauth.Refresh(ctx, tok.RefreshToken)
	if err != nil {
		log.Printf("token refresh failed: %v", err)
		var oe *hardcover.OAuthError
		if errors.As(err, &oe) && !hardcover.IsTransient(err) {
			// Refused (e.g. invalid_grant): the user must sign in again.
			return "", fmt.Errorf("%w (%v)", hardcover.ErrUnauthorized, err)
		}
		return "", err // no network or server problem: try again later
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = tok.RefreshToken
	}
	if err := a.store.Save(nt); err != nil {
		return "", err
	}
	log.Printf("token refreshed")
	return nt.AccessToken, nil
}

func (a *app) needRefresh(t *hardcover.Token) bool {
	return t.Expired(5*time.Minute) && t.RefreshToken != "" && a.oauth.ClientID != ""
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

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..." // eips has no "…" glyph (UNVERIFIED)
}
