package smsapibg

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the production API. Override it with [WithBaseURL]
	// for a proxy or a test server.
	DefaultBaseURL = "https://api.smsapi.bg/"

	// DefaultTimeout bounds a whole request on the default HTTP client, so a
	// stalled connection can never hang a caller that forgot a context
	// deadline.
	DefaultTimeout = 15 * time.Second

	// DefaultUserAgent identifies this library to smsapi.bg unless the caller
	// names its own application with [WithUserAgent].
	DefaultUserAgent = "smsapibg-client-go"

	// maxBodyBytes caps how much of a response is read, so a broken or
	// hostile server cannot exhaust memory. Real responses are tiny.
	maxBodyBytes = 1 << 20
)

// Client talks to the smsapi.bg API with one token. It is safe for
// concurrent use and holds no mutable state after [New] returns.
type Client struct {
	token     *secret // a pointer: fmt prints a nested pointer as an address, never its target
	baseURL   *url.URL
	http      *http.Client
	userAgent string
	now       func() time.Time // the clock Send checks ExpiresAt against
}

// Option changes how [New] builds a [Client].
type Option func(*Client) error

// New returns a client that authenticates with token, an API token from
// the smsapi.bg customer portal. Give the token only the scopes you use
// (sending SMS, and reading the profile if you check the balance).
//
// New refuses an empty token or one with whitespace, an invalid option, and
// a non-HTTPS base URL that is not on loopback. Its errors never include the
// token.
func New(token string, opts ...Option) (*Client, error) {
	if token == "" {
		return nil, errors.New("smsapibg: the API token is empty")
	}
	if strings.ContainsFunc(token, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return nil, errors.New("smsapibg: the API token contains whitespace or control characters")
	}
	base, err := parseBaseURL(DefaultBaseURL)
	if err != nil {
		return nil, err
	}
	c := &Client{
		token:     &secret{value: token},
		baseURL:   base,
		http:      defaultHTTPClient(),
		userAgent: DefaultUserAgent,
		now:       time.Now,
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// WithBaseURL points the client at another API root, such as a proxy or an
// httptest server. It must be https, or http on a loopback host, and must not
// carry userinfo (user:password@), which could leak through logs. Its errors
// never echo the URL.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		u, err := parseBaseURL(raw)
		if err != nil {
			return err
		}
		c.baseURL = u
		return nil
	}
}

// WithHTTPClient makes the client send through hc, for a caller that wants
// its own transport, proxy or instrumentation. hc is used as given: if its
// Timeout is zero, only the request context bounds a call.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return errors.New("smsapibg: WithHTTPClient: the *http.Client is nil")
		}
		c.http = hc
		return nil
	}
}

// WithUserAgent names the calling application in the User-Agent header, so
// smsapi.bg support can tell its traffic apart.
func WithUserAgent(ua string) Option {
	return func(c *Client) error {
		if strings.TrimSpace(ua) == "" {
			return errors.New("smsapibg: WithUserAgent: the user agent is empty")
		}
		c.userAgent = ua
		return nil
	}
}

// String keeps the token out of anything that prints a client.
func (c Client) String() string {
	if c.baseURL == nil {
		return "smsapibg.Client{}"
	}
	return "smsapibg.Client{" + c.baseURL.Redacted() + "}"
}

// GoString keeps the token out of %#v.
func (c Client) GoString() string { return c.String() }

// Format prints String for every verb, so a verb String does not serve
// (%d, %t, %9.3f, ...) cannot make fmt walk the fields. %p of a Client
// value still walks them, which is why the token is behind a pointer.
func (c Client) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, c.String()) }

// LogValue keeps the token out of log/slog.
func (c Client) LogValue() slog.Value { return slog.StringValue(c.String()) }

// secret holds the token and refuses to print or encode it, with any verb.
// Only reveal returns the value, and only call uses it, for the
// Authorization header.
type secret struct{ value string }

const redacted = "[redacted]"

func (*secret) String() string               { return redacted }
func (*secret) GoString() string             { return redacted }
func (*secret) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, redacted) }
func (*secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (*secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (s *secret) reveal() string             { return s.value }

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("smsapibg: the base URL must be an absolute URL")
	}
	if u.User != nil {
		return nil, errors.New("smsapibg: the base URL must not carry credentials (userinfo); the token is the only credential")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return nil, errors.New("smsapibg: the base URL must use https (http only on loopback, for tests)")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func defaultHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: DefaultTimeout, Transport: tr}
}

// endpoint resolves path (no leading slash) against the base URL.
func (c *Client) endpoint(path string) string {
	return c.baseURL.JoinPath(path).String()
}

// call makes one request and decodes a successful JSON answer into out. An
// error body is an *APIError whether it comes with a 2xx or not; a 2xx body
// that does not decode wraps ErrUnexpectedResponse. Errors never carry the
// token or the raw body.
func (c *Client) call(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return fmt.Errorf("smsapibg: build %s request: %w", path, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Authorization", "Bearer "+c.token.reveal())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("smsapibg: %s request failed: %w", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("smsapibg: read %s response: %w", path, err)
	}

	if apiErr := decodeAPIError(raw, resp.StatusCode); apiErr != nil {
		return apiErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{HTTPStatus: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUnexpectedResponse, path, err)
	}
	return nil
}

// decodeAPIError returns the error a body describes, or nil if it describes
// none. smsapi.bg uses a number for "error" on sms.do and a string on the
// newer REST endpoints such as /profile.
func decodeAPIError(raw []byte, status int) *APIError {
	var probe struct {
		Error          json.RawMessage `json:"error"`
		Message        string          `json:"message"`
		InvalidNumbers []InvalidNumber `json:"invalid_numbers"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return nil
	}
	e := &APIError{Message: probe.Message, InvalidNumbers: probe.InvalidNumbers, HTTPStatus: status}
	var n looseInt
	var s string
	switch {
	case len(probe.Error) == 0 || string(probe.Error) == "null":
		return nil
	case json.Unmarshal(probe.Error, &n) == nil:
		e.Code = int(n)
	case json.Unmarshal(probe.Error, &s) == nil:
		e.Name = s
	default:
		return nil
	}
	if e.Code == 0 && e.Name == "" {
		return nil
	}
	return e
}
