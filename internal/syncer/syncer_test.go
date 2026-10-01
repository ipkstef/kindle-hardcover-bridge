package syncer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ipkstef/kindle-hardcover-bridge/internal/book"
	"github.com/ipkstef/kindle-hardcover-bridge/internal/hardcover"
)

// fakeHC answers GraphQL by the first matching operation name and records
// the order of operations.
type fakeHC struct {
	ops   []string
	vars  []map[string]any
	reply map[string]string
}

func (f *fakeHC) client(t *testing.T) *hardcover.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.Unmarshal(b, &req)
		for _, op := range []string{"insert_reading_journal", "update_user_book_read", "insert_user_book_read", "update_user_book",
			"insert_user_book", "user_books(where: {id", "user_books(where: {user_id", "me {", "editions(", "search("} {
			if strings.Contains(req.Query, op) {
				f.ops = append(f.ops, op)
				f.vars = append(f.vars, req.Variables)
				w.Write([]byte(f.reply[op]))
				return
			}
		}
		t.Errorf("unexpected query %s", req.Query)
	}))
	t.Cleanup(srv.Close)
	return &hardcover.Client{HTTP: srv.Client(), Endpoint: srv.URL,
		Token: func(context.Context) (string, error) { return "tok", nil },
		Sleep: func(context.Context, time.Duration) error { return nil }}
}

const ubJSON = `{"id":9,"book_id":500,"status_id":%d,"edition":{"id":33,"pages":400},
	"book":{"title":"Red Rising","pages":382,"cached_contributors":[{"author":{"name":"Pierce Brown"}}]},
	"user_book_reads":[{"id":77,"started_at":"2026-09-01","finished_at":null,"progress_pages":300,"edition_id":33}]}`

func newFake(status int) *fakeHC {
	ub := strings.Replace(ubJSON, "%d", string(rune('0'+status)), 1)
	return &fakeHC{reply: map[string]string{
		"me {":                       `{"data":{"me":[{"id":1,"username":"u","account_privacy_setting_id":1}]}}`,
		"user_books(where: {user_id": `{"data":{"user_books":[` + ub + `]}}`,
		"user_books(where: {id":      `{"data":{"user_books":[` + ub + `]}}`,
		"update_user_book_read":      `{"data":{"update_user_book_read":{"error":null,"user_book_read":{"id":77}}}}`,
		"update_user_book":           `{"data":{"update_user_book":{"error":null,"user_book":` + ub + `}}}`,
		"insert_reading_journal":     `{"data":{"insert_reading_journal":{"id":555,"errors":null}}}`,
	}}
}

var redRising = book.Local{Key: "k", Title: "Red Rising (The Red Rising Trilogy, Book 1)",
	Authors: []string{"Pierce Brown"}, Path: "/none/x.kfx", MimeType: "application/x-kfx-ebook"}

func TestFinishOrder(t *testing.T) {
	f := newFake(2)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent = 99.5
	out, err := s.Sync(context.Background(), &l)
	if err != nil || !out.Finished || out.Kind != Sent || out.Page != 400 {
		t.Fatalf("got %+v %v", out, err)
	}
	// The read is finished before the status changes.
	iRead, iStatus := -1, -1
	for i, op := range f.ops {
		switch op {
		case "update_user_book_read":
			iRead = i
			if f.vars[i]["finishedAt"] == nil || f.vars[i]["pages"] != float64(400) {
				t.Errorf("finish vars %v", f.vars[i])
			}
		case "update_user_book":
			iStatus = i
			if f.vars[i]["status"] != float64(hardcover.StatusRead) {
				t.Errorf("status vars %v", f.vars[i])
			}
		}
	}
	if iRead < 0 || iStatus < 0 || iRead > iStatus {
		t.Fatalf("order %v", f.ops)
	}
}

func TestFinishByReadState(t *testing.T) {
	f := newFake(2)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent, l.ReadState = 60, 2 // user marked it read on the Kindle
	out, err := s.Sync(context.Background(), &l)
	if err != nil || !out.Finished {
		t.Fatalf("got %+v %v", out, err)
	}
}

func TestAlreadyRead(t *testing.T) {
	f := newFake(3)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent = 100
	out, err := s.Sync(context.Background(), &l)
	if err != nil || out.Kind != Unchanged {
		t.Fatalf("got %+v %v", out, err)
	}
	for _, op := range f.ops {
		if strings.HasPrefix(op, "update") || strings.HasPrefix(op, "insert") {
			t.Fatalf("wrote %v", f.ops)
		}
	}
}

func TestProgressNotFinished(t *testing.T) {
	f := newFake(2)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent = 98.9 // 1 is "almost read" on the Kindle, not finished
	l.ReadState = 1
	out, err := s.Sync(context.Background(), &l)
	if err != nil || out.Finished || out.Page != 395 {
		t.Fatalf("got %+v %v", out, err)
	}
}

func TestFinishNotTwiceSameDay(t *testing.T) {
	f := newFake(2)
	today := time.Now().Format("2006-01-02")
	ub := strings.Replace(ubJSON, "%d", "2", 1)
	ub = strings.Replace(ub, `"finished_at":null`, `"finished_at":"`+today+`"`, 1)
	f.reply["user_books(where: {user_id"] = `{"data":{"user_books":[` + ub + `]}}`
	f.reply["user_books(where: {id"] = `{"data":{"user_books":[` + ub + `]}}`
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent, l.ReadState = 98.22, 2
	if _, err := s.Sync(context.Background(), &l); err != nil {
		t.Fatal(err)
	}
	for _, op := range f.ops {
		if op == "update_user_book_read" || op == "insert_user_book_read" {
			t.Fatalf("touched reads: %v", f.ops)
		}
	}
}

// Book Read on Hardcover, back at 2 % on the Kindle → re-read: status
// Currently Reading, then progress on the open read.
func TestRereadOnRestart(t *testing.T) {
	f := newFake(3)
	f.reply["update_user_book"] = strings.Replace(f.reply["update_user_book"], `"status_id":3`, `"status_id":2`, 1)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent = 2
	out, err := s.Sync(context.Background(), &l)
	if err != nil || out.Added != "re-read started" || out.Page != 8 {
		t.Fatalf("got %+v %v", out, err)
	}
	iStatus, iRead := -1, -1
	for i, op := range f.ops {
		switch op {
		case "update_user_book":
			iStatus = i
		case "update_user_book_read":
			iRead = i
		}
	}
	if iStatus < 0 || iRead < 0 || iStatus > iRead {
		t.Fatalf("order %v", f.ops)
	}
}

func TestMissCache(t *testing.T) {
	s := &Syncer{}
	if _, ok := s.missed("k"); ok {
		t.Fatal("missed before any miss")
	}
	s.miss("k")
	if _, ok := s.missed("k"); !ok {
		t.Fatal("miss not remembered")
	}
	s.misses["k"] = time.Now().Add(-missTTL - time.Minute)
	if _, ok := s.missed("k"); ok {
		t.Fatal("miss not expired")
	}
}

// Same page as last sent: no API call at all.
func TestUnchangedPageNoCall(t *testing.T) {
	f := newFake(2)
	cache := &BookCache{Path: t.TempDir() + "/bookmap.json"}
	s := &Syncer{C: f.client(t), Cache: cache, Logf: t.Logf}
	l := redRising
	l.Percent = 98.9
	if _, err := s.Sync(context.Background(), &l); err != nil {
		t.Fatal(err)
	}
	n := len(f.ops)
	l.Percent = 98.95 // same page 395/400
	out, err := s.Sync(context.Background(), &l)
	if err != nil || out.Kind != Unchanged || len(f.ops) != n {
		t.Fatalf("got %+v %v, calls %v", out, err, f.ops[n:])
	}
}

// Reading on in a Paused book moves it to Currently Reading.
func TestPausedMovesToReading(t *testing.T) {
	f := newFake(4)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent = 80
	out, err := s.Sync(context.Background(), &l)
	if err != nil || out.Added != "moved from Paused" {
		t.Fatalf("got %+v %v", out, err)
	}
}

// A percent from the sidecar never finishes a book.
func TestSidecarOnlyNeverFinishes(t *testing.T) {
	f := newFake(2)
	s := &Syncer{C: f.client(t), Logf: t.Logf}
	l := redRising
	l.Percent, l.SidecarOnly = 99.6, true
	out, err := s.Sync(context.Background(), &l)
	if err != nil || out.Finished {
		t.Fatalf("got %+v %v", out, err)
	}
}
