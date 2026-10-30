package smsapibg

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
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
// concurrent use and holds no mutable state after [New] returns. Make it
// with New: the methods of a zero Client return an error.
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
// Timeout is zero, only the request context bounds a call, and it follows
// redirects unless its CheckRedirect says otherwise (the default client
// refuses them, because following one replays the POST).
//
// A transport error counts as ErrOutcomeUnknown unless it is clearly from
// before sending (dial, DNS, TLS handshake or certificate, see
// ErrOutcomeUnknown). A transport that wraps those errors in its own types,
// or reports them differently, makes more errors "unknown": the safe side.
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

// defaultHTTPClient does not follow redirects: the API never redirects, and
// following one would replay a POST, possibly with the Authorization header.
// A redirect comes back as an *APIError with its 3xx status.
func defaultHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Timeout:       DefaultTimeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// errNotBuilt is what a method returns on a Client that New did not make.
var errNotBuilt = errors.New("smsapibg: the Client was not made with New")

// ready reports whether c was made by New, so a zero or nil Client returns
// an error instead of panicking.
func (c *Client) ready() error {
	if c == nil || c.token == nil || c.baseURL == nil || c.http == nil || c.now == nil {
		return errNotBuilt
	}
	return nil
}

// endpoint resolves path (no leading slash) against the base URL.
func (c *Client) endpoint(path string) string {
	return c.baseURL.JoinPath(path).String()
}

// call makes one request and decodes a successful JSON answer into out,
// returning that answer exactly as read (a copy the caller owns). An error
// body is an *APIError whether it comes with a 2xx or not; a 2xx body that
// does not decode wraps ErrUnexpectedResponse. The error's text never carries
// the token or the raw body; only [APIError.Raw] holds the body.
func (c *Client) call(ctx context.Context, method, path string, form url.Values, out any) (json.RawMessage, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), body)
	if err != nil {
		return nil, fmt.Errorf("smsapibg: build %s request: %w", path, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Authorization", "Bearer "+c.token.reveal())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	// A context already done means the request never leaves.
	if err := ctx.Err(); err != nil {
		return nil, &transportError{op: path + " request not sent", err: err}
	}
	// wrote turns true once the request headers are on the wire. It is only
	// ever a signal toward "unknown": a transport that drops the request
	// context never fires it, so its silence proves nothing.
	var wrote atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { wrote.Store(true) },
	}))

	resp, err := c.http.Do(req)
	if err != nil {
		sent := wrote.Load() || !clearlyNotSent(err)
		return nil, &transportError{op: path + " request failed", err: err, sent: sent}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, &transportError{op: "read " + path + " response", err: err, sent: true}
	}

	if apiErr := decodeAPIError(raw, resp.StatusCode); apiErr != nil {
		return nil, apiErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Keep the body only when it is JSON, so Raw always marshals.
		var kept json.RawMessage
		if json.Valid(raw) {
			kept = bytes.Clone(raw)
		}
		return nil, &APIError{HTTPStatus: resp.StatusCode, Message: http.StatusText(resp.StatusCode), Raw: kept}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(out); err != nil {
		return nil, &unexpectedError{path: path, reason: describeDecodeError(err)}
	}
	return bytes.Clone(raw), nil
}

// transportError is a request that got no HTTP answer. It unwraps to the
// cause (so context.Canceled and timeouts match errors.Is), and it is
// ErrOutcomeUnknown when the request had already been sent.
type transportError struct {
	op   string
	err  error
	sent bool
}

func (e *transportError) Error() string { return "smsapibg: " + e.op + ": " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }
func (e *transportError) Is(target error) bool {
	return target == ErrOutcomeUnknown && e.sent
}

// clearlyNotSent reports whether a transport error can only have happened
// before any of the request reached smsapi.bg: the connection was never
// made (dial, DNS) or never became a trusted TLS session. Every other
// error, including ones a custom transport invents, may come after the
// request was sent, so the outcome is unknown.
func clearlyNotSent(err error) bool {
	var dns *net.DNSError
	var op *net.OpError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &dns):
		return true
	case errors.As(err, &op) && op.Op == "dial":
		return true
	case errors.As(err, &verify), errors.As(err, &record),
		errors.As(err, &authority), errors.As(err, &host), errors.As(err, &invalid):
		return true
	}
	return false
}

// unexpectedError is a 2xx answer that could not be read. The request was
// answered, so smsapi.bg may well have acted on it: it is also
// ErrOutcomeUnknown.
type unexpectedError struct {
	path, reason string
}

func (e *unexpectedError) Error() string {
	return "smsapibg: unexpected response: " + e.path + ": " + e.reason
}
func (e *unexpectedError) Is(target error) bool {
	return target == ErrUnexpectedResponse || target == ErrOutcomeUnknown
}

// describeDecodeError says where a body failed to decode without quoting
// any of it: decoder errors can carry values from the body.
func describeDecodeError(err error) string {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return "invalid JSON at byte " + strconv.FormatInt(syntax.Offset, 10)
	case errors.As(err, &typ) && typ.Field != "":
		return "field " + typ.Field + " has the wrong type"
	default:
		return "the answer is not the expected JSON"
	}
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
	if len(raw) > 0 {
		e.Raw = bytes.Clone(raw)
	}
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
