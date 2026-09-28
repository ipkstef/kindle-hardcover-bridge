package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), Endpoint: srv.URL,
		Token: func(context.Context) (string, error) { return "tok", nil }}
}

func TestCurrentlyReading(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if !strings.Contains(req.Query, "status_id: {_eq: 2}") || req.Variables["userId"] != float64(7) {
			t.Errorf("bad request %+v", req)
		}
		w.Write([]byte(`{"data":{"user_books":[{
			"id":11,"book_id":22,"edition":{"id":33,"pages":400},
			"book":{"title":"A Parade of Horribles","pages":350,
			        "cached_contributors":[{"author":{"name":"Matt Dinniman"},"contribution":null}]},
			"user_book_reads":[
				{"id":1,"finished_at":"2025-01-01","progress_pages":400,"edition_id":33,"edition":{"id":33,"pages":400}},
				{"id":2,"finished_at":null,"started_at":"2026-09-01","progress_pages":20,"edition_id":44,"edition":{"id":44,"pages":500}}
			]}]}}`))
	})
	ubs, err := c.CurrentlyReading(context.Background(), 7)
	if err != nil || len(ubs) != 1 {
		t.Fatalf("%v %v", err, ubs)
	}
	ub := ubs[0]
	if got := ub.Authors(); len(got) != 1 || got[0] != "Matt Dinniman" {
		t.Errorf("authors %v", got)
	}
	if r := ub.CurrentRead(); r == nil || r.ID != 2 {
		t.Errorf("current read %+v", r)
	}
	if p, ed := ub.Pages(); p != 500 || ed == nil || *ed != 44 {
		t.Errorf("pages %d %v", p, ed)
	}
}

func TestUnauthorized(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"Could not verify JWT: invalid-jwt"}]}`))
	})
	if _, err := c.Me(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestUpdateReadError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"update_user_book_read":{"error":"nope","user_book_read":null}}}`))
	})
	if _, err := c.UpdateReadProgress(context.Background(), 1, 10, nil, nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestRetry429AndMeCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Body as seen on the device (2026-09-28).
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"Too Many Requests","message":"API rate limit exceeded for tier 'Free'. Try again in 1 seconds."}`))
			return
		}
		w.Write([]byte(`{"data":{"me":[{"id":7,"username":"u"}]}}`))
	}))
	defer srv.Close()
	var slept []time.Duration
	c := &Client{HTTP: srv.Client(), Endpoint: srv.URL,
		Token: func(context.Context) (string, error) { return "tok", nil },
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	me, err := c.Me(context.Background())
	if err != nil || me.ID != 7 || calls != 2 {
		t.Fatalf("me %+v err %v calls %d", me, err, calls)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("slept %v", slept)
	}
	// Cached: no new request.
	if _, err := c.Me(context.Background()); err != nil || calls != 2 {
		t.Fatalf("cache miss: calls %d", calls)
	}
}
