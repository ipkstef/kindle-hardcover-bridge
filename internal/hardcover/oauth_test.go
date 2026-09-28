package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeviceFlow(t *testing.T) {
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "cid" || r.Form.Get("scope") != "read:me" {
			t.Errorf("bad device form: %v", r.Form)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc", "user_code": "ABCD-1234",
			"verification_uri": "https://hardcover.app/link", "expires_in": 600, "interval": 5,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("device_code") != "dc" {
			t.Errorf("bad token form: %v", r.Form)
		}
		polls++
		switch polls {
		case 1:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"authorization_pending"}`))
		case 2:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"slow_down"}`))
		default:
			w.Write([]byte(`{"access_token":"hc_at_x","refresh_token":"hc_rt_y","expires_in":3600}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var slept []time.Duration
	o := &OAuth{HTTP: srv.Client(), ClientID: "cid", DeviceEndpoint: srv.URL + "/device", TokenEndpoint: srv.URL + "/token",
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}

	dc, err := o.RequestDeviceCode(context.Background(), "read:me")
	if err != nil || dc.UserCode != "ABCD-1234" {
		t.Fatalf("device code: %v %+v", err, dc)
	}
	tok, err := o.PollToken(context.Background(), dc)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "hc_at_x" || tok.RefreshToken != "hc_rt_y" || tok.Expired(time.Minute) {
		t.Fatalf("bad token %+v", tok)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	if len(slept) != 3 || slept[0] != want[0] || slept[1] != want[1] || slept[2] != want[2] {
		t.Fatalf("intervals %v, want %v", slept, want)
	}
}

func TestPollDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()
	o := &OAuth{HTTP: srv.Client(), TokenEndpoint: srv.URL,
		Sleep: func(context.Context, time.Duration) error { return nil }}
	_, err := o.PollToken(context.Background(), &DeviceCode{DeviceCode: "dc", Interval: 1})
	oe, ok := err.(*OAuthError)
	if !ok || oe.Code != "access_denied" {
		t.Fatalf("got %v", err)
	}
}
