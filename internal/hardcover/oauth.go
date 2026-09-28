package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default endpoints. From https://api.hardcover.app/.well-known/oauth-authorization-server
// (confirmed on a real Kindle, 2026-09-28).
const (
	DeviceEndpoint  = "https://api.hardcover.app/oauth2/device"
	TokenEndpoint   = "https://api.hardcover.app/oauth2/token"
	GraphQLEndpoint = "https://api.hardcover.app/v1/graphql"
)

// DefaultScope is Hardcover's official "E-Reader / Sync Client" preset
// (hardcover-docs src/data/scopePresets.ts). write:library also covers
// journal entries and ratings. No write:reviews (user decision).
const DefaultScope = "read:catalog read:library write:library read:me:content"

// DeviceCode is the response of the device authorization request (RFC 8628).
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Token is an OAuth token set. ExpiresAt is computed on receipt.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	Scope        string    `json:"scope"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Expired reports if the access token expires within the given margin.
func (t *Token) Expired(margin time.Duration) bool {
	return t.ExpiresAt.IsZero() || time.Now().Add(margin).After(t.ExpiresAt)
}

// OAuthError is an error response from the token endpoint.
type OAuthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
	Status      int    `json:"-"`
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("oauth: %s: %s", e.Code, e.Description)
	}
	return fmt.Sprintf("oauth: %s (HTTP %d)", e.Code, e.Status)
}

// OAuth runs the device flow against Hardcover.
type OAuth struct {
	HTTP           *http.Client
	ClientID       string
	DeviceEndpoint string
	TokenEndpoint  string
	// Sleep is replaced in tests.
	Sleep func(context.Context, time.Duration) error
}

// NewOAuth returns an OAuth with the default endpoints.
func NewOAuth(hc *http.Client, clientID string) *OAuth {
	return &OAuth{HTTP: hc, ClientID: clientID, DeviceEndpoint: DeviceEndpoint, TokenEndpoint: TokenEndpoint}
}

// RequestDeviceCode starts the device flow.
func (o *OAuth) RequestDeviceCode(ctx context.Context, scope string) (*DeviceCode, error) {
	form := url.Values{"client_id": {o.ClientID}, "scope": {scope}}
	var dc DeviceCode
	if err := o.post(ctx, o.DeviceEndpoint, form, &dc); err != nil {
		return nil, err
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, errors.New("oauth: device response has no code")
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

// PollToken polls the token endpoint until the user approves, denies, or the
// code expires.
func (o *OAuth) PollToken(ctx context.Context, dc *DeviceCode) (*Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if dc.ExpiresIn > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(dc.ExpiresIn)*time.Second)
		defer cancel()
	}
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {dc.DeviceCode},
		"client_id":   {o.ClientID},
	}
	for {
		if err := o.sleep(ctx, interval); err != nil {
			return nil, fmt.Errorf("oauth: code expired or cancelled: %w", err)
		}
		tok, err := o.token(ctx, form)
		var oe *OAuthError
		switch {
		case err == nil:
			return tok, nil
		case errors.As(err, &oe) && oe.Code == "authorization_pending":
			continue
		case errors.As(err, &oe) && oe.Code == "slow_down":
			interval += 5 * time.Second
			continue
		default:
			return nil, err
		}
	}
}

// Refresh gets a new token set with a refresh token.
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	return o.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {o.ClientID},
	})
}

func (o *OAuth) token(ctx context.Context, form url.Values) (*Token, error) {
	var t Token
	if err := o.post(ctx, o.TokenEndpoint, form, &t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" {
		return nil, errors.New("oauth: token response has no access_token")
	}
	if t.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return &t, nil
}

func (o *OAuth) post(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		oe := &OAuthError{Status: resp.StatusCode}
		if json.Unmarshal(body, oe) != nil || oe.Code == "" {
			oe.Code = "http_error"
			oe.Description = strings.TrimSpace(string(body))
		}
		return oe
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("oauth: bad JSON from %s: %w", endpoint, err)
	}
	return nil
}

func (o *OAuth) sleep(ctx context.Context, d time.Duration) error {
	if o.Sleep != nil {
		return o.Sleep(ctx, d)
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
