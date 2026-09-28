package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Status IDs for user_books. From the Hardcover KOReader plugin source
// (UNVERIFIED against official docs).
const (
	StatusWantToRead = 1
	StatusReading    = 2
	StatusRead       = 3
	StatusDNF        = 5
)

// Client is a small Hardcover GraphQL client.
type Client struct {
	HTTP     *http.Client
	Endpoint string
	// Token returns a valid access token (it may refresh it first).
	Token func(context.Context) (string, error)
}

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

// Do runs one GraphQL request and decodes "data" into out.
func (c *Client) Do(ctx context.Context, query string, vars map[string]any, out any) error {
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
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
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
}

// Me returns the signed-in user.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var r struct {
		Me []Me `json:"me"`
	}
	if err := c.Do(ctx, `query { me { id username } }`, nil, &r); err != nil {
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
	ID      int      `json:"id"`
	BookID  int      `json:"book_id"`
	Edition *Edition `json:"edition"`
	Book    struct {
		Title string `json:"title"`
		Pages int    `json:"pages"`
		// Shape is UNVERIFIED; parsed leniently in Authors.
		Contributors json.RawMessage `json:"cached_contributors"`
	} `json:"book"`
	Reads []Read `json:"user_book_reads"`
}

// Authors returns the author names of the book.
func (ub *UserBook) Authors() []string {
	var list []struct {
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if json.Unmarshal(ub.Book.Contributors, &list) != nil {
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
	edition { id pages }
	book { title pages cached_contributors }
	user_book_reads(order_by: {id: asc}) {
		id started_at finished_at progress_pages edition_id
		edition { id pages }
	}`

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
