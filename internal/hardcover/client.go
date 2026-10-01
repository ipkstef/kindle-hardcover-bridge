package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status IDs for user_books (hardcover-docs UserBooks.mdx).
const (
	StatusWantToRead = 1
	StatusReading    = 2
	StatusRead       = 3
	StatusPaused     = 4
	StatusDNF        = 5
	StatusIgnored    = 6
)

// Client is a small Hardcover GraphQL client. Use one Client per process:
// it holds the rate limiter and the cached "me".
type Client struct {
	HTTP     *http.Client
	Endpoint string
	// Token returns a valid access token (it may refresh it first).
	Token func(context.Context) (string, error)
	// Sleep is replaced in tests.
	Sleep func(context.Context, time.Duration) error
	// Calls counts requests sent (for logs and tests).
	Calls int

	mu     sync.Mutex
	tokens float64   // rate limiter bucket
	last   time.Time // last refill
	me     *Me
	meAt   time.Time
	// offlineAt: set when a request failed for lack of network. Until
	// Online() is called (Wi-Fi back) or offlineRetry passed, requests fail
	// at once with ErrTransient: no work, no radio use while offline.
	offlineAt time.Time
}

// offlineRetry: while offline, try the network again after this long even
// without a "Wi-Fi back" event (the event may be missed).
const offlineRetry = 10 * time.Minute

// ErrOffline is returned while the client waits for the network.
var ErrOffline = fmt.Errorf("%w: offline, waiting for Wi-Fi", ErrTransient)

// Online tells the client the network is back (Kindle "connectionAvailable").
func (c *Client) Online() {
	c.mu.Lock()
	c.offlineAt = time.Time{}
	c.mu.Unlock()
}

// Offline reports if the client is waiting for the network.
func (c *Client) Offline() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.offlineAt.IsZero() && time.Since(c.offlineAt) < offlineRetry
}

// Rate limit: Hardcover free tier allows 60 requests/min, burst 10
// (hardcover-docs Getting-Started.mdx). We stay below it.
const (
	rateBurst   = 5
	ratePerSec  = 0.9
	maxRetry429 = 3
	meTTL       = time.Hour
)

// wait takes one token from the bucket, sleeping if needed.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	if c.last.IsZero() {
		c.tokens, c.last = rateBurst, now
	}
	c.tokens = min(rateBurst, c.tokens+now.Sub(c.last).Seconds()*ratePerSec)
	c.last = now
	var d time.Duration
	if c.tokens < 1 {
		d = time.Duration((1 - c.tokens) / ratePerSec * float64(time.Second))
	}
	c.tokens--
	c.mu.Unlock()
	if d > 0 {
		return c.sleep(ctx, d)
	}
	return nil
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var retryInRe = regexp.MustCompile(`(?i)try again in (\d+) second`)

// GraphQLError is one error from a GraphQL response.
type GraphQLError struct {
	Message string `json:"message"`
}

// Errors is a list of GraphQL errors.
type Errors []GraphQLError

func (e Errors) Error() string {
	msgs := make([]string, len(e))
	for i, m := range e {
		msgs[i] = m.Message
	}
	return "graphql: " + strings.Join(msgs, "; ")
}

// ErrUnauthorized means the token was rejected. The user must sign in again.
var ErrUnauthorized = errors.New("hardcover: token rejected, sign in again")

// ErrTransient marks a failure that can go away by itself: no network, a
// timeout, HTTP 5xx or 429. The caller keeps the work and tries again later;
// it must never treat it as "not found".
var ErrTransient = errors.New("temporary failure")

// IsTransient reports if err is a temporary failure (see ErrTransient). A
// cancelled or timed-out context also counts.
func IsTransient(err error) bool {
	return errors.Is(err, ErrTransient) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func transient(err error) error { return fmt.Errorf("%w: %w", ErrTransient, err) }

// netError marks a failure to reach the server at all (DNS, connect,
// TLS): the device is offline. It turns on offline mode.
type netError struct{ err error }

func (e netError) Error() string { return e.err.Error() }
func (e netError) Unwrap() error { return e.err }

// noNetwork reports errors that mean the device has no network: DNS
// failure or no route / connection refused while dialing. A timeout while
// waiting for the answer is a slow server, not offline (device log
// 2026-09-28: one slow answer showed "No connection" with Wi-Fi up).
func noNetwork(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial" && !op.Timeout()
}

// errRetry429 carries the wait time of a 429 answer.
type errRetry429 struct{ wait time.Duration }

func (e errRetry429) Error() string { return "hardcover: HTTP 429 (rate limit)" }
func (e errRetry429) Unwrap() error { return ErrTransient }

// Do runs one GraphQL request and decodes "data" into out. It waits for the
// rate limiter and retries after HTTP 429.
func (c *Client) Do(ctx context.Context, query string, vars map[string]any, out any) error {
	if c.Offline() {
		return ErrOffline
	}
	for i := 0; ; i++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		err := c.do(ctx, query, vars, out)
		var nerr netError
		if errors.As(err, &nerr) {
			c.mu.Lock()
			c.offlineAt = time.Now().Round(0) // wall clock: counts sleep time too
			c.mu.Unlock()
		}
		var r429 errRetry429
		if !errors.As(err, &r429) || i >= maxRetry429 {
			return err
		}
		if err := c.sleep(ctx, r429.wait); err != nil {
			return err
		}
	}
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	c.mu.Lock()
	c.Calls++
	c.mu.Unlock()
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "kindle-hardcover-bridge (https://github.com/ipkstef/kindle-hardcover-bridge)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() == nil && noNetwork(err) {
			return transient(netError{err})
		}
		return transient(err) // e.g. a slow server (timeout): not "offline"
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return transient(err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := 2 * time.Second
		if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			wait = time.Duration(ra+1) * time.Second
		} else if m := retryInRe.FindSubmatch(raw); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil {
				wait = time.Duration(n+1) * time.Second
			}
		}
		return errRetry429{wait: min(wait, time.Minute)}
	}
	if resp.StatusCode/100 == 5 {
		return transient(fmt.Errorf("hardcover: HTTP %d: %.200s", resp.StatusCode, raw))
	}
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors Errors          `json:"errors"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("hardcover: HTTP %d, bad JSON: %.200s", resp.StatusCode, raw)
	}
	if len(r.Errors) > 0 {
		for _, e := range r.Errors {
			if strings.Contains(e.Message, "invalid-jwt") || strings.Contains(e.Message, "Unable to verify token") {
				return ErrUnauthorized
			}
		}
		return r.Errors
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hardcover: HTTP %d: %.200s", resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Data, out)
}

// Me is the signed-in user.
type Me struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	// PrivacyID is the account default privacy for new shelf entries.
	PrivacyID *int `json:"account_privacy_setting_id"`
}

// Me returns the signed-in user. Cached for an hour.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	c.mu.Lock()
	if c.me != nil && time.Since(c.meAt) < meTTL {
		m := c.me
		c.mu.Unlock()
		return m, nil
	}
	c.mu.Unlock()
	m, err := c.fetchMe(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.me, c.meAt = m, time.Now()
	c.mu.Unlock()
	return m, nil
}

func (c *Client) fetchMe(ctx context.Context) (*Me, error) {
	var r struct {
		Me []Me `json:"me"`
	}
	if err := c.Do(ctx, `query { me { id username account_privacy_setting_id } }`, nil, &r); err != nil {
		return nil, err
	}
	if len(r.Me) == 0 {
		return nil, errors.New("hardcover: empty me response")
	}
	return &r.Me[0], nil
}

// Edition is the part of an edition we need.
type Edition struct {
	ID    int `json:"id"`
	Pages int `json:"pages"`
}

// Read is one user_book_read.
type Read struct {
	ID            int      `json:"id"`
	StartedAt     *string  `json:"started_at"`
	FinishedAt    *string  `json:"finished_at"`
	ProgressPages *int     `json:"progress_pages"`
	EditionID     *int     `json:"edition_id"`
	Edition       *Edition `json:"edition"`
}

// UserBook is a book on the user's shelf.
type UserBook struct {
	ID       int      `json:"id"`
	BookID   int      `json:"book_id"`
	StatusID int      `json:"status_id"`
	Edition  *Edition `json:"edition"`
	Book     struct {
		Title string `json:"title"`
		Pages int    `json:"pages"`
		// Shape is UNVERIFIED; parsed leniently in Authors.
		Contributors json.RawMessage `json:"cached_contributors"`
	} `json:"book"`
	Reads []Read `json:"user_book_reads"`
}

// Authors returns the author names of the book.
func (ub *UserBook) Authors() []string { return contributorNames(ub.Book.Contributors) }

// contributorNames parses cached_contributors:
// [{"author":{"name":"..."},"contribution":null}] (shape UNVERIFIED; lenient).
func contributorNames(raw json.RawMessage) []string {
	var list []struct {
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var names []string
	for _, c := range list {
		if c.Author.Name != "" {
			names = append(names, c.Author.Name)
		}
	}
	return names
}

// CurrentRead returns the newest read that is not finished, or nil.
func (ub *UserBook) CurrentRead() *Read {
	for i := len(ub.Reads) - 1; i >= 0; i-- {
		if ub.Reads[i].FinishedAt == nil {
			return &ub.Reads[i]
		}
	}
	return nil
}

// Pages returns the page count to use for percent → page: the current read's
// edition, then the user book's edition, then the book.
func (ub *UserBook) Pages() (pages int, editionID *int) {
	if r := ub.CurrentRead(); r != nil && r.Edition != nil && r.Edition.Pages > 0 {
		return r.Edition.Pages, &r.Edition.ID
	}
	if ub.Edition != nil && ub.Edition.Pages > 0 {
		return ub.Edition.Pages, &ub.Edition.ID
	}
	return ub.Book.Pages, nil
}

const userBookFields = `
	id
	book_id
	status_id
	edition { id pages }
	book { title pages cached_contributors }
	user_book_reads(order_by: {id: asc}) {
		id started_at finished_at progress_pages edition_id
		edition { id pages }
	}`

// UserBookForBook returns the user's shelf entry for one book, or nil.
func (c *Client) UserBookForBook(ctx context.Context, userID, bookID int) (*UserBook, error) {
	var r struct {
		UBs []UserBook `json:"user_books"`
	}
	q := `query ($userId: Int!, $bookId: Int!) {
		user_books(where: {user_id: {_eq: $userId}, book_id: {_eq: $bookId}}, limit: 1) {` + userBookFields + `}
	}`
	if err := c.Do(ctx, q, map[string]any{"userId": userID, "bookId": bookID}, &r); err != nil {
		return nil, err
	}
	if len(r.UBs) == 0 {
		return nil, nil
	}
	return &r.UBs[0], nil
}

// libraryFields: only what the title + author match needs. The full
// userBookFields (all reads, editions) made the response for a large
// library several MB (review 2026-09-28).
const libraryFields = `
	id
	book_id
	status_id
	book { title cached_contributors }`

// Library returns all books on the user's shelves (any status), with only
// the fields for matching: no editions, no reads.
func (c *Client) Library(ctx context.Context, userID int) ([]UserBook, error) {
	var r struct {
		UserBooks []UserBook `json:"user_books"`
	}
	q := `query ($userId: Int!) {
		user_books(where: {user_id: {_eq: $userId}}, limit: 5000) {` + libraryFields + `}
	}`
	if err := c.Do(ctx, q, map[string]any{"userId": userID}, &r); err != nil {
		return nil, err
	}
	return r.UserBooks, nil
}

// CurrentlyReading returns the user's books with status "Currently Reading".
func (c *Client) CurrentlyReading(ctx context.Context, userID int) ([]UserBook, error) {
	var r struct {
		UserBooks []UserBook `json:"user_books"`
	}
	q := `query ($userId: Int!) {
		user_books(where: {user_id: {_eq: $userId}, status_id: {_eq: 2}}) {` + userBookFields + `}
	}`
	if err := c.Do(ctx, q, map[string]any{"userId": userID}, &r); err != nil {
		return nil, err
	}
	return r.UserBooks, nil
}

type readResult struct {
	Error *string `json:"error"`
	Read  *Read   `json:"user_book_read"`
}

// UpdateReadProgress sets progress_pages on an existing read.
func (c *Client) UpdateReadProgress(ctx context.Context, readID, pages int, editionID *int, startedAt *string) (*Read, error) {
	var r struct {
		R readResult `json:"update_user_book_read"`
	}
	q := `mutation ($id: Int!, $pages: Int, $editionId: Int, $startedAt: date) {
		update_user_book_read(id: $id, object: {progress_pages: $pages, edition_id: $editionId, started_at: $startedAt}) {
			error
			user_book_read { id started_at finished_at progress_pages edition_id }
		}
	}`
	vars := map[string]any{"id": readID, "pages": pages, "editionId": editionID, "startedAt": startedAt}
	if err := c.Do(ctx, q, vars, &r); err != nil {
		return nil, err
	}
	return r.R.check()
}

// FinishRead sets progress_pages and finished_at on a read.
func (c *Client) FinishRead(ctx context.Context, readID, pages int, editionID *int, startedAt *string, finishedAt string) (*Read, error) {
	var r struct {
		R readResult `json:"update_user_book_read"`
	}
	q := `mutation ($id: Int!, $pages: Int, $editionId: Int, $startedAt: date, $finishedAt: date) {
		update_user_book_read(id: $id, object: {progress_pages: $pages, edition_id: $editionId,
			started_at: $startedAt, finished_at: $finishedAt}) {
			error
			user_book_read { id started_at finished_at progress_pages edition_id }
		}
	}`
	vars := map[string]any{"id": readID, "pages": pages, "editionId": editionID, "startedAt": startedAt, "finishedAt": finishedAt}
	if err := c.Do(ctx, q, vars, &r); err != nil {
		return nil, err
	}
	return r.R.check()
}

// InsertRead starts a new read with progress_pages.
func (c *Client) InsertRead(ctx context.Context, userBookID, pages int, editionID *int, startedAt string) (*Read, error) {
	var r struct {
		R readResult `json:"insert_user_book_read"`
	}
	q := `mutation ($id: Int!, $pages: Int, $editionId: Int, $startedAt: date) {
		insert_user_book_read(user_book_id: $id, user_book_read: {progress_pages: $pages, edition_id: $editionId, started_at: $startedAt}) {
			error
			user_book_read { id started_at finished_at progress_pages edition_id }
		}
	}`
	vars := map[string]any{"id": userBookID, "pages": pages, "editionId": editionID, "startedAt": startedAt}
	if err := c.Do(ctx, q, vars, &r); err != nil {
		return nil, err
	}
	return r.R.check()
}

func (r readResult) check() (*Read, error) {
	if r.Error != nil && *r.Error != "" {
		return nil, fmt.Errorf("hardcover: %s", *r.Error)
	}
	if r.Read == nil {
		return nil, errors.New("hardcover: no user_book_read in response")
	}
	return r.Read, nil
}

// BookHit is a catalog book.
type BookHit struct {
	ID           int             `json:"id"`
	Title        string          `json:"title"`
	ReleaseYear  *int            `json:"release_year"`
	UsersRead    *int            `json:"users_read_count"`
	Contributors json.RawMessage `json:"cached_contributors"`
}

// Authors returns the author names.
func (b *BookHit) Authors() []string { return contributorNames(b.Contributors) }

// EditionHit is a catalog edition with its book.
type EditionHit struct {
	ID     int     `json:"id"`
	Pages  int     `json:"pages"`
	BookID int     `json:"book_id"`
	Book   BookHit `json:"book"`
}

// Edition ID fields we may filter on.
var editionFields = map[string]bool{"asin": true, "isbn_13": true, "isbn_10": true}

// EditionsBy returns editions where field (asin, isbn_13, isbn_10) equals value.
func (c *Client) EditionsBy(ctx context.Context, field, value string) ([]EditionHit, error) {
	if !editionFields[field] {
		return nil, fmt.Errorf("hardcover: bad edition field %q", field)
	}
	var r struct {
		Editions []EditionHit `json:"editions"`
	}
	q := `query ($v: String!) {
		editions(where: {` + field + `: {_eq: $v}}, limit: 10) {
			id pages book_id
			book { id title release_year users_read_count cached_contributors }
		}
	}`
	if err := c.Do(ctx, q, map[string]any{"v": value}, &r); err != nil {
		return nil, err
	}
	return r.Editions, nil
}

// SearchBooks runs a catalog search and returns the matching books.
func (c *Client) SearchBooks(ctx context.Context, query string, limit int) ([]BookHit, error) {
	var r struct {
		Search struct {
			IDs []json.RawMessage `json:"ids"`
		} `json:"search"`
	}
	q := `query ($q: String!, $n: Int!) {
		search(query: $q, query_type: "Book", per_page: $n, page: 1) { ids }
	}`
	if err := c.Do(ctx, q, map[string]any{"q": query, "n": limit}, &r); err != nil {
		return nil, err
	}
	var ids []int
	for _, raw := range r.Search.IDs {
		var n int
		if json.Unmarshal(raw, &n) == nil {
			ids = append(ids, n)
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if _, err := fmt.Sscan(s, &n); err == nil {
				ids = append(ids, n)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var b struct {
		Books []BookHit `json:"books"`
	}
	bq := `query ($ids: [Int!]) {
		books(where: {id: {_in: $ids}}) { id title release_year users_read_count cached_contributors }
	}`
	if err := c.Do(ctx, bq, map[string]any{"ids": ids}, &b); err != nil {
		return nil, err
	}
	// Keep search order.
	pos := map[int]int{}
	for i, id := range ids {
		pos[id] = i
	}
	out := make([]BookHit, len(ids))
	found := make([]bool, len(ids))
	for _, bk := range b.Books {
		if i, ok := pos[bk.ID]; ok {
			out[i], found[i] = bk, true
		}
	}
	res := out[:0]
	for i := range out {
		if found[i] {
			res = append(res, out[i])
		}
	}
	return res, nil
}

type userBookResult struct {
	Error *string   `json:"error"`
	UB    *UserBook `json:"user_book"`
}

func (r userBookResult) check() (*UserBook, error) {
	if r.Error != nil && *r.Error != "" {
		return nil, fmt.Errorf("hardcover: %s", *r.Error)
	}
	if r.UB == nil {
		return nil, errors.New("hardcover: no user_book in response")
	}
	return r.UB, nil
}

// InsertUserBook puts a book on the user's shelf with a status.
func (c *Client) InsertUserBook(ctx context.Context, bookID, statusID int, editionID *int, privacyID int) (*UserBook, error) {
	var r struct {
		R userBookResult `json:"insert_user_book"`
	}
	obj := map[string]any{"book_id": bookID, "status_id": statusID, "privacy_setting_id": privacyID}
	if editionID != nil {
		obj["edition_id"] = *editionID
	}
	q := `mutation ($object: UserBookCreateInput!) {
		insert_user_book(object: $object) { error user_book {` + userBookFields + `} }
	}`
	if err := c.Do(ctx, q, map[string]any{"object": obj}, &r); err != nil {
		return nil, err
	}
	return r.R.check()
}

// SetStatus changes the shelf status of a user book.
func (c *Client) SetStatus(ctx context.Context, userBookID, statusID int) (*UserBook, error) {
	var r struct {
		R userBookResult `json:"update_user_book"`
	}
	q := `mutation ($id: Int!, $status: Int!) {
		update_user_book(id: $id, object: {status_id: $status}) { error user_book {` + userBookFields + `} }
	}`
	if err := c.Do(ctx, q, map[string]any{"id": userBookID, "status": statusID}, &r); err != nil {
		return nil, err
	}
	return r.R.check()
}

// SetRating sets the star rating (0.5–5) of a user book. It follows the
// shelf entry's privacy. Scope: write:library.
func (c *Client) SetRating(ctx context.Context, userBookID int, rating float64) (*UserBook, error) {
	var r struct {
		R userBookResult `json:"update_user_book"`
	}
	q := `mutation ($id: Int!, $rating: numeric) {
		update_user_book(id: $id, object: {rating: $rating}) { error user_book {` + userBookFields + `} }
	}`
	if err := c.Do(ctx, q, map[string]any{"id": userBookID, "rating": rating}, &r); err != nil {
		return nil, err
	}
	return r.R.check()
}

// DefaultEdition returns the book's default ebook edition, else its default
// physical edition, else nil.
func (c *Client) DefaultEdition(ctx context.Context, bookID int) (*Edition, error) {
	var r struct {
		B *struct {
			Ebook    *Edition `json:"default_ebook_edition"`
			Physical *Edition `json:"default_physical_edition"`
		} `json:"books_by_pk"`
	}
	q := `query ($id: Int!) { books_by_pk(id: $id) {
		default_ebook_edition { id pages }
		default_physical_edition { id pages }
	} }`
	if err := c.Do(ctx, q, map[string]any{"id": bookID}, &r); err != nil {
		return nil, err
	}
	switch {
	case r.B == nil:
		return nil, nil
	case r.B.Ebook != nil && r.B.Ebook.Pages > 0:
		return r.B.Ebook, nil
	case r.B.Physical != nil && r.B.Physical.Pages > 0:
		return r.B.Physical, nil
	}
	return nil, nil
}

// Journal privacy IDs (hardcover-docs ReadingJournals.mdx).
const (
	PrivacyPublic    = 1
	PrivacyFollowers = 2
	PrivacyPrivate   = 3
)

// JournalEntry is a reading journal entry (note, quote, ...).
type JournalEntry struct {
	BookID    int
	EditionID *int
	Event     string // "note", "quote"
	Entry     string
	PrivacyID int
	ActionAt  string         // date, "2006-01-02"
	Metadata  map[string]any // e.g. {"position":{"type":"pages","value":44,"possible":400}}
}

// InsertJournal adds a reading journal entry and returns its id.
// Scope: write:library.
func (c *Client) InsertJournal(ctx context.Context, e JournalEntry) (int, error) {
	obj := map[string]any{
		"book_id": e.BookID, "event": e.Event, "entry": e.Entry,
		"privacy_setting_id": e.PrivacyID, "tags": []any{},
	}
	if e.EditionID != nil {
		obj["edition_id"] = *e.EditionID
	}
	if e.ActionAt != "" {
		obj["action_at"] = e.ActionAt
	}
	if e.Metadata != nil {
		obj["metadata"] = e.Metadata
	}
	var r struct {
		R struct {
			ID     *int     `json:"id"`
			Errors []string `json:"errors"`
		} `json:"insert_reading_journal"`
	}
	q := `mutation ($object: ReadingJournalCreateType!) {
		insert_reading_journal(object: $object) { id errors }
	}`
	if err := c.Do(ctx, q, map[string]any{"object": obj}, &r); err != nil {
		return 0, err
	}
	if len(r.R.Errors) > 0 {
		return 0, fmt.Errorf("hardcover: %s", strings.Join(r.R.Errors, "; "))
	}
	if r.R.ID == nil {
		return 0, errors.New("hardcover: no journal id in response")
	}
	return *r.R.ID, nil
}
