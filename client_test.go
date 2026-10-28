package smsapibg

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testToken is a placeholder. It is not, and must never become, a real token.
const testToken = "test-token-not-real-0123456789"

// fixedNow is the clock every test client runs on.
var fixedNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// newTestClient starts an httptest server with handler and returns a client
// pointed at it, on a fixed clock.
func newTestClient(t *testing.T, handler http.HandlerFunc, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(testToken, append([]Option{WithBaseURL(srv.URL)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.now = func() time.Time { return fixedNow }
	return c
}

func TestNewRefusesAnEmptyToken(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") succeeded, want an error")
	}
}

func TestNewRefusesATokenWithWhitespaceWithoutEchoingIt(t *testing.T) {
	for _, token := range []string{"abc def", "abc\r\nX-Evil: 1", " abc", "abc\t"} {
		_, err := New(token)
		if err == nil {
			t.Errorf("New(%q) succeeded, want an error", token)
			continue
		}
		if strings.Contains(err.Error(), "abc") {
			t.Errorf("New error %q echoes the token", err)
		}
	}
}

func TestNewUsesTheProductionBaseURLByDefault(t *testing.T) {
	c, err := New(testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.baseURL.String(); got != "https://api.smsapi.bg/" {
		t.Errorf("base URL = %q, want https://api.smsapi.bg/", got)
	}
}

func TestNewRefusesABaseURLThatIsNotHTTPS(t *testing.T) {
	for _, raw := range []string{"http://api.smsapi.bg/", "ftp://api.smsapi.bg/", "api.smsapi.bg", "https://", "::"} {
		if _, err := New(testToken, WithBaseURL(raw)); err == nil {
			t.Errorf("WithBaseURL(%q) accepted, want an error", raw)
		}
	}
}

func TestABaseURLWithCredentialsIsRefused(t *testing.T) {
	for _, raw := range []string{
		"https://alice:hunter2@proxy.example.com/",
		"https://alice@proxy.example.com/",
		"http://alice:hunter2@127.0.0.1:8080/",
	} {
		_, err := New(testToken, WithBaseURL(raw))
		if err == nil {
			t.Errorf("WithBaseURL with userinfo accepted, want an error")
			continue
		}
		for _, leak := range []string{"alice", "hunter2", "proxy.example.com", "127.0.0.1"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("WithBaseURL error %q echoes the URL (%q)", err, leak)
			}
		}
	}
}

func TestStringNeverPrintsUserinfoOfTheBaseURL(t *testing.T) {
	c, err := New(testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// WithBaseURL refuses userinfo; String must hold even if a URL with it
	// ever got in, so set one directly.
	c.baseURL.User = url.UserPassword("user", "hunter2")
	for _, out := range []string{c.String(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", *c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(out, "hunter2") {
			t.Errorf("printed client %q contains the base URL password", out)
		}
	}
}

func TestNewAllowsPlainHTTPOnlyOnLoopbackForTests(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:8080", "http://localhost:1234/", "http://[::1]:99"} {
		if _, err := New(testToken, WithBaseURL(raw)); err != nil {
			t.Errorf("WithBaseURL(%q): %v", raw, err)
		}
	}
}

func TestWithBaseURLKeepsAPathPrefix(t *testing.T) {
	c, err := New(testToken, WithBaseURL("https://proxy.example.com/smsapi"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.endpoint("sms.do"); got != "https://proxy.example.com/smsapi/sms.do" {
		t.Errorf("endpoint = %q, want https://proxy.example.com/smsapi/sms.do", got)
	}
}

func TestNewRefusesANilHTTPClient(t *testing.T) {
	if _, err := New(testToken, WithHTTPClient(nil)); err == nil {
		t.Fatal("WithHTTPClient(nil) accepted, want an error")
	}
}

func TestNewRefusesAnEmptyUserAgent(t *testing.T) {
	if _, err := New(testToken, WithUserAgent("  ")); err == nil {
		t.Fatal("WithUserAgent(blank) accepted, want an error")
	}
}

func TestWithHTTPClientIsUsedAsGiven(t *testing.T) {
	hc := &http.Client{Timeout: 3 * time.Second}
	c, err := New(testToken, WithHTTPClient(hc))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http != hc {
		t.Error("the client does not use the given *http.Client")
	}
}

func TestTheDefaultHTTPClientHasATimeoutAndRequiresTLS12(t *testing.T) {
	c, err := New(testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http.Timeout <= 0 {
		t.Errorf("default timeout = %v, want > 0", c.http.Timeout)
	}
	if c.http.Timeout != DefaultTimeout {
		t.Errorf("default timeout = %v, want DefaultTimeout (%v)", c.http.Timeout, DefaultTimeout)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("default transport is %T, want *http.Transport", c.http.Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Error("default transport does not require TLS 1.2 or later")
	}
	if tr == http.DefaultTransport {
		t.Error("default transport is the shared http.DefaultTransport, want a private clone")
	}
}

func TestTheClientNeverPrintsItsToken(t *testing.T) {
	c, err := New(testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		for _, v := range []any{c, *c, c.token} {
			if out := fmt.Sprintf(verb, v); strings.Contains(out, testToken) {
				t.Errorf("fmt %s of %T prints the token: %s", verb, v, out)
			}
		}
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("client", "client", c, "value", *c, "token", c.token)
	if strings.Contains(buf.String(), testToken) {
		t.Errorf("slog prints the token: %s", buf.String())
	}
}

func TestTheClientSendsTheTokenOnlyInTheAuthorizationHeader(t *testing.T) {
	var seen *http.Request
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r
		_ = r.ParseForm()
		_, _ = w.Write([]byte(`{"points":1}`))
	})
	if _, err := c.Profile(t.Context()); err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the bearer token", got)
	}
	if strings.Contains(seen.URL.String(), testToken) {
		t.Errorf("the token is in the URL: %s", seen.URL)
	}
	for name, values := range seen.Header {
		if name == "Authorization" {
			continue
		}
		for _, v := range values {
			if strings.Contains(v, testToken) {
				t.Errorf("header %s carries the token", name)
			}
		}
	}
}

func TestTheClientSendsItsUserAgent(t *testing.T) {
	var ua string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"points":1}`))
	}, WithUserAgent("notskip-backend/1.0"))
	if _, err := c.Profile(t.Context()); err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if ua != "notskip-backend/1.0" {
		t.Errorf("User-Agent = %q, want notskip-backend/1.0", ua)
	}
}

func TestTheDefaultUserAgentNamesThisLibrary(t *testing.T) {
	var ua string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"points":1}`))
	})
	if _, err := c.Profile(t.Context()); err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if ua != DefaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", ua, DefaultUserAgent)
	}
}

func TestATokenEncodedAsJSONIsRedacted(t *testing.T) {
	c, err := New(testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := json.Marshal(struct{ Token any }{c.token})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(out), testToken) || !strings.Contains(string(out), "redacted") {
		t.Errorf("JSON of the token = %s, want it redacted", out)
	}
}

func TestAZeroClientPrintsWithoutPanicking(t *testing.T) {
	if got := (Client{}).String(); got != "smsapibg.Client{}" {
		t.Errorf("String() = %q, want smsapibg.Client{}", got)
	}
}
