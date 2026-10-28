package smsapibg

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const queuedBody = `{"count":1,"list":[{"id":"1460969715572091219","points":0.16,"number":"359888123456",` +
	`"date_sent":1460969712,"submitted_number":"359888123456","status":"QUEUE"}]}`

// capture records the one request a fake server receives.
type capture struct {
	method, path, rawQuery string
	header                 http.Header
	form                   url.Values
}

// serve answers every request with status and body, recording it in got.
func serve(t *testing.T, got *capture, status int, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		*got = capture{method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery, header: r.Header.Clone(), form: r.PostForm}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestASendPostsTheFormWithTheBearerToken(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, queuedBody))

	if _, err := c.Send(t.Context(), Message{To: "359888123456", Text: "Your code is 123456"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/sms.do" {
		t.Errorf("request = %s %s, want POST /sms.do", got.method, got.path)
	}
	if got.rawQuery != "" {
		t.Errorf("query = %q, want none (everything goes in the form)", got.rawQuery)
	}
	if v := got.header.Get("Authorization"); v != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the bearer token", v)
	}
	if v := got.header.Get("Content-Type"); v != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", v)
	}
	want := url.Values{"to": {"359888123456"}, "message": {"Your code is 123456"}, "format": {"json"}}
	if got.form.Encode() != want.Encode() {
		t.Errorf("form = %s, want exactly %s", got.form.Encode(), want.Encode())
	}
}

func TestASendWithEveryOptionPostsEveryField(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, queuedBody))
	expires := fixedNow.Add(2 * time.Hour)

	_, err := c.Send(t.Context(), Message{
		To:        "359888123456",
		Text:      "Код: 123456",
		From:      "Note-Skip.1",
		Test:      true,
		Normalize: true,
		NoUnicode: true,
		MaxParts:  2,
		Idx:       "verify-42",
		CheckIdx:  true,
		ExpiresAt: expires,
		NotifyURL: "https://api.example.com/sms/dlr",
		Details:   true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := url.Values{
		"to":              {"359888123456"},
		"message":         {"Код: 123456"},
		"format":          {"json"},
		"from":            {"Note-Skip.1"},
		"test":            {"1"},
		"normalize":       {"1"},
		"nounicode":       {"1"},
		"max_parts":       {"2"},
		"idx":             {"verify-42"},
		"check_idx":       {"1"},
		"expiration_date": {strconv.FormatInt(expires.Unix(), 10)},
		"notify_url":      {"https://api.example.com/sms/dlr"},
		"details":         {"1"},
	}
	if got.form.Encode() != want.Encode() {
		t.Errorf("form = %s\nwant   %s", got.form.Encode(), want.Encode())
	}
}

func TestASendStripsALeadingPlusOrDoubleZeroFromTheNumber(t *testing.T) {
	for _, to := range []string{"+359888123456", "00359888123456", "359888123456"} {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusOK, queuedBody))
		if _, err := c.Send(t.Context(), Message{To: to, Text: "hi"}); err != nil {
			t.Fatalf("Send(%q): %v", to, err)
		}
		if v := got.form.Get("to"); v != "359888123456" {
			t.Errorf("Send(%q) posted to=%q, want 359888123456", to, v)
		}
	}
}

func TestASendRefusesAnInvalidMessageWithoutCallingTheAPI(t *testing.T) {
	ok := Message{To: "359888123456", Text: "hi"}
	cases := []struct {
		name  string
		edit  func(*Message)
		field string
	}{
		{"empty number", func(m *Message) { m.To = "" }, "to"},
		{"only a plus", func(m *Message) { m.To = "+" }, "to"},
		{"letters in the number", func(m *Message) { m.To = "35988812345a" }, "to"},
		{"spaces in the number", func(m *Message) { m.To = "359 888 123456" }, "to"},
		{"a national number", func(m *Message) { m.To = "0888123456" }, "to"},
		{"more than 15 digits", func(m *Message) { m.To = "3598881234567890" }, "to"},
		{"empty text", func(m *Message) { m.Text = "" }, "message"},
		{"blank text", func(m *Message) { m.Text = " \n\t" }, "message"},
		{"text that is not utf-8", func(m *Message) { m.Text = "bad \xff byte" }, "message"},
		{"sender name too long", func(m *Message) { m.From = "ABCDEFGHIJKL" }, "from"},
		{"sender name with a bad character", func(m *Message) { m.From = "Not_Skip" }, "from"},
		{"sender name in cyrillic", func(m *Message) { m.From = "НотСкип" }, "from"},
		{"max parts below one", func(m *Message) { m.MaxParts = -1 }, "max_parts"},
		{"max parts above nine", func(m *Message) { m.MaxParts = 10 }, "max_parts"},
		{"idx too long", func(m *Message) { m.Idx = strings.Repeat("a", 256) }, "idx"},
		{"idx with a comma", func(m *Message) { m.Idx = "a,b" }, "idx"},
		{"idx with a control character", func(m *Message) { m.Idx = "a\nb" }, "idx"},
		{"check idx without an idx", func(m *Message) { m.CheckIdx = true }, "idx"},
		{"expiry too soon", func(m *Message) { m.ExpiresAt = fixedNow.Add(14 * time.Minute) }, "expiration_date"},
		{"expiry in the past", func(m *Message) { m.ExpiresAt = fixedNow.Add(-time.Hour) }, "expiration_date"},
		{"expiry too far", func(m *Message) { m.ExpiresAt = fixedNow.Add(72*time.Hour + time.Second) }, "expiration_date"},
		{"notify url not absolute", func(m *Message) { m.NotifyURL = "/dlr" }, "notify_url"},
		{"notify url not http", func(m *Message) { m.NotifyURL = "ftp://example.com/dlr" }, "notify_url"},
		{"notify url unparsable", func(m *Message) { m.NotifyURL = "https://exa mple.com/%zz" }, "notify_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
			m := ok
			tc.edit(&m)
			_, err := c.Send(t.Context(), m)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Send error = %v, want a *ValidationError", err)
			}
			if ve.Field != tc.field {
				t.Errorf("Field = %q, want %q", ve.Field, tc.field)
			}
			if called {
				t.Error("the API was called for an invalid message")
			}
		})
	}
}

func TestASendAcceptsTheEdgesOfEachRule(t *testing.T) {
	cases := []Message{
		{To: "1", Text: "x"},
		{To: "359888123456", Text: "x", From: "ABCDEFGHIJK"},
		{To: "359888123456", Text: "x", From: "a-b.c 1"},
		{To: "359888123456", Text: "x", MaxParts: 1},
		{To: "359888123456", Text: "x", MaxParts: 9},
		{To: "359888123456", Text: "x", Idx: strings.Repeat("a", 255), CheckIdx: true},
		{To: "359888123456", Text: "x", ExpiresAt: fixedNow.Add(15 * time.Minute)},
		{To: "359888123456", Text: "x", ExpiresAt: fixedNow.Add(72 * time.Hour)},
		{To: "359888123456", Text: "x", NotifyURL: "http://example.com/dlr"},
	}
	for _, m := range cases {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusOK, queuedBody))
		if _, err := c.Send(t.Context(), m); err != nil {
			t.Errorf("Send(%+v): %v", m, err)
		}
	}
}

func TestASendReturnsTheQueuedResult(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, queuedBody))

	res, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := Result{
		ID:     "1460969715572091219",
		Points: 0.16,
		Number: "359888123456",
		Status: "QUEUE",
		SentAt: time.Unix(1460969712, 0),
	}
	if res != want {
		t.Errorf("Result = %+v\nwant     %+v", res, want)
	}
}

func TestASendReadsThePartsWhenDetailsAreRequested(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"parts on the message", `{"count":1,"list":[{"id":"1","points":0.32,"number":"359888123456","status":"QUEUE","parts":2}]}`},
		{"parts at the top level", `{"count":1,"list":[{"id":"1","points":0.32,"number":"359888123456","status":"QUEUE"}],"message":"hi","length":170,"parts":2}`},
	}
	for _, tc := range cases {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusOK, tc.body))
		res, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi", Details: true})
		if err != nil {
			t.Fatalf("%s: Send: %v", tc.name, err)
		}
		if res.Parts != 2 {
			t.Errorf("%s: Parts = %d, want 2", tc.name, res.Parts)
		}
	}
}

func TestASendToleratesNumbersSentAsStrings(t *testing.T) {
	var got capture
	body := `{"count":"1","list":[{"id":1460969715572091219,"points":"0.16","number":"359888123456","date_sent":"1460969712","status":"QUEUE","parts":"1"}]}`
	c := newTestClient(t, serve(t, &got, http.StatusOK, body))
	res, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ID != "1460969715572091219" || res.Points != 0.16 || res.Parts != 1 || res.SentAt.Unix() != 1460969712 {
		t.Errorf("Result = %+v", res)
	}
}

func TestASendWithoutADateLeavesSentAtZero(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, `{"count":1,"list":[{"id":"1","points":0.16,"number":"359888123456","status":"QUEUE"}]}`))
	res, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res.SentAt.IsZero() {
		t.Errorf("SentAt = %v, want zero", res.SentAt)
	}
}

func TestASend200WithAnErrorBodyIsAnAPIError(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, `{"error":103,"message":"Insufficient credits on Your account"}`))

	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("Send error = %v, want ErrInsufficientCredits", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Send error is %T, want *APIError", err)
	}
	if apiErr.Code != CodeInsufficientCredits || apiErr.HTTPStatus != http.StatusOK || apiErr.Message != "Insufficient credits on Your account" {
		t.Errorf("APIError = %+v", *apiErr)
	}
}

func TestASendDecodesTheInvalidNumbers(t *testing.T) {
	var got capture
	body := `{"error":13,"message":"No correct phone numbers","invalid_numbers":[` +
		`{"number":"359888123456","submitted_number":"359888123456","message":"Invalid phone number"}]}`
	c := newTestClient(t, serve(t, &got, http.StatusOK, body))

	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, ErrInvalidNumber) {
		t.Fatalf("Send error = %v, want an *APIError that is ErrInvalidNumber", err)
	}
	want := InvalidNumber{Number: "359888123456", SubmittedNumber: "359888123456", Message: "Invalid phone number"}
	if len(apiErr.InvalidNumbers) != 1 || apiErr.InvalidNumbers[0] != want {
		t.Errorf("InvalidNumbers = %+v, want [%+v]", apiErr.InvalidNumbers, want)
	}
}

func TestARetryRefusedAsADuplicateIdxIsAlreadySentNotAnInvalidNumber(t *testing.T) {
	var got capture
	body := `{"invalid_numbers":[{"number":"359888123456","submitted_number":"359888123456","message":"Not unique idx"}],` +
		`"error":53,"message":"Not unique idx parameter, message with the same idx has been already sent and &check_idx=1."}`
	c := newTestClient(t, serve(t, &got, http.StatusOK, body))

	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi", Idx: "verify-42", CheckIdx: true})
	if !errors.Is(err, ErrDuplicateIdx) {
		t.Fatalf("Send error = %v, want ErrDuplicateIdx", err)
	}
	if errors.Is(err, ErrInvalidNumber) {
		t.Error("a duplicate idx is reported as an invalid number")
	}
}

func TestASendNon2xxWithAJSONErrorIsAnAPIError(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusTooManyRequests, `{"error":203,"message":"Too many requests. Please try again later."}`))

	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Send error = %v, want *APIError", err)
	}
	if apiErr.Code != CodeTooManyRequests || apiErr.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("APIError = %+v", *apiErr)
	}
	if !errors.Is(err, ErrRateLimited) || !errors.Is(err, ErrTemporary) || !apiErr.Temporary() {
		t.Errorf("error %v is not rate limited and temporary", err)
	}
}

func TestASendNon2xxWithoutAJSONBodyIsAnAPIErrorWithTheStatus(t *testing.T) {
	for _, body := range []string{"", "<html>Bad Gateway</html>", `{"message":"no error field"}`} {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusBadGateway, body))
		_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("body %q: Send error = %v, want *APIError", body, err)
		}
		if apiErr.HTTPStatus != http.StatusBadGateway || apiErr.Code != 0 {
			t.Errorf("body %q: APIError = %+v", body, *apiErr)
		}
		if !errors.Is(err, ErrTemporary) {
			t.Errorf("body %q: a 502 is not ErrTemporary", body)
		}
		if strings.Contains(err.Error(), "html") {
			t.Errorf("body %q: the error echoes the body: %v", body, err)
		}
	}
}

func TestASendMalformedJSONIsAnUnexpectedResponse(t *testing.T) {
	for _, body := range []string{"OK:1460969715572091219:0.16", `{"count":1,"list":[`, `{"count":1,"list":{}}`} {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusOK, body))
		_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
		if !errors.Is(err, ErrUnexpectedResponse) {
			t.Errorf("body %q: Send error = %v, want ErrUnexpectedResponse", body, err)
		}
	}
}

func TestASendWithAnEmptyListIsAnUnexpectedResponse(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, `{"count":0,"list":[]}`))
	if _, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"}); !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("Send error = %v, want ErrUnexpectedResponse", err)
	}
}

// newHangingClient returns a client whose server never answers until the
// client goes away or the test ends. The server drains the body first, so it
// notices a client that disconnects, and the test's cleanup releases the
// handler before it closes the server.
func newHangingClient(t *testing.T, arrived chan<- struct{}, opts ...Option) *Client {
	t.Helper()
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if arrived != nil {
			close(arrived)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, opts...)
	t.Cleanup(func() { close(release) })
	return c
}

func TestASendStopsWhenTheContextIsCancelled(t *testing.T) {
	arrived := make(chan struct{})
	c := newHangingClient(t, arrived)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { <-arrived; cancel() }()

	_, err := c.Send(ctx, Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}
}

func TestASendTimesOutOnASlowServer(t *testing.T) {
	c := newHangingClient(t, nil, WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}))

	start := time.Now()
	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if err == nil {
		t.Fatal("Send succeeded against a server that never answers")
	}
	var timeout interface{ Timeout() bool }
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Errorf("Send error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Send took %v, want about the 50ms timeout", elapsed)
	}
}

func TestTheOutcomeIsUnknownWhenTheRequestReachedTheServerAndNoAnswerCame(t *testing.T) {
	arrived := make(chan struct{})
	c := newHangingClient(t, arrived)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { <-arrived; cancel() }()
	_, err := c.Send(ctx, Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Errorf("cancelled after the request was sent: error = %v, want ErrOutcomeUnknown", err)
	}

	c = newHangingClient(t, nil, WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}))
	_, err = c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Errorf("timed out waiting for the answer: error = %v, want ErrOutcomeUnknown", err)
	}
}

func TestTheOutcomeIsUnknownWhenTheConnectionDropsAfterTheRequest(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		_ = conn.Close()
	})
	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("Send error = %v, want ErrOutcomeUnknown", err)
	}
}

func TestTheOutcomeIsKnownWhenTheRequestNeverLeft(t *testing.T) {
	// Nothing listens on port 1: the connection is refused before a byte is sent.
	c, err := New(testToken, WithBaseURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if err == nil || errors.Is(err, ErrOutcomeUnknown) {
		t.Errorf("connection refused: error = %v, want an error that is not ErrOutcomeUnknown", err)
	}

	called := false
	c = newTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = c.Send(ctx, Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrOutcomeUnknown) || called {
		t.Errorf("cancelled before sending: error = %v (called %v), want context.Canceled and not ErrOutcomeUnknown", err, called)
	}
}

func TestAnUnreadableAnswerToASendIsAnUnknownOutcome(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusOK, "OK:1460969715572091219:0.16"))
	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, ErrUnexpectedResponse) || !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("Send error = %v, want ErrUnexpectedResponse and ErrOutcomeUnknown", err)
	}
}

func TestA5xxWithoutACodeIsAnUnknownOutcome(t *testing.T) {
	var got capture
	c := newTestClient(t, serve(t, &got, http.StatusBadGateway, "<html>Bad Gateway</html>"))
	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, ErrTemporary) {
		t.Fatalf("Send error = %v, want ErrOutcomeUnknown and ErrTemporary", err)
	}
}

func TestASendHonoursAContextDeadline(t *testing.T) {
	c := newHangingClient(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Send(ctx, Message{To: "359888123456", Text: "hi"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send error = %v, want context.DeadlineExceeded", err)
	}
}

func TestASendErrorNeverContainsTheToken(t *testing.T) {
	bodies := []struct {
		status int
		body   string
	}{
		{http.StatusOK, `{"error":101,"message":"Invalid authorization info"}`},
		{http.StatusInternalServerError, "boom"},
		{http.StatusOK, "not json"},
	}
	for _, b := range bodies {
		var got capture
		c := newTestClient(t, serve(t, &got, b.status, b.body))
		_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
		if err == nil || strings.Contains(err.Error(), testToken) {
			t.Errorf("Send error = %v, want an error without the token", err)
		}
	}
	// A transport failure: nothing listens on this port.
	c, err := New(testToken, WithBaseURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("Send error = %v, want an error without the token", err)
	}
}

func TestASendValidationErrorNeverContainsTheNumberOrText(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := c.Send(t.Context(), Message{To: "35988812345x", Text: "secret code 987654"})
	if err == nil || strings.Contains(err.Error(), "35988812345") {
		t.Errorf("Send error = %v, want one without the number", err)
	}
	_, err = c.Send(t.Context(), Message{To: "359888123456", Text: "secret code 987654", From: "Bad_Name"})
	if err == nil || strings.Contains(err.Error(), "987654") || strings.Contains(err.Error(), "Bad_Name") {
		t.Errorf("Send error = %v, want one without the text or sender", err)
	}
}

func TestASuccessWithoutAMessageIDIsAnUnexpectedResponse(t *testing.T) {
	for _, body := range []string{
		`{"count":1,"list":[{"points":0.16,"number":"359888123456","status":"QUEUE"}]}`,
		`{"count":1,"list":[{"id":"","points":0.16,"number":"359888123456","status":"QUEUE"}]}`,
	} {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusOK, body))
		if _, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"}); !errors.Is(err, ErrUnexpectedResponse) {
			t.Errorf("body %s: Send error = %v, want ErrUnexpectedResponse", body, err)
		}
	}
}

func TestOddOptionalFieldsDoNotTurnASentMessageIntoAnError(t *testing.T) {
	var got capture
	body := `{"count":1,"list":[{"id":"1","points":"","number":359888123456,"date_sent":1460969712.0,` +
		`"status":null,"parts":""}],"parts":"n/a","length":{}}`
	c := newTestClient(t, serve(t, &got, http.StatusOK, body))
	res, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi", Details: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := Result{ID: "1", Number: "359888123456", SentAt: time.Unix(1460969712, 0)}
	if res != want {
		t.Errorf("Result = %+v\nwant     %+v", res, want)
	}
}

func TestAnUnreadableAnswerNeverQuotesTheBody(t *testing.T) {
	for _, body := range []string{
		`{"count":1,"list":[{"id":{"secretvalue":1}}]}`,
		`{"count":1,"list":"secretvalue"}`,
	} {
		var got capture
		c := newTestClient(t, serve(t, &got, http.StatusOK, body))
		_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
		if !errors.Is(err, ErrUnexpectedResponse) || strings.Contains(err.Error(), "secretvalue") {
			t.Errorf("body %s: Send error = %v, want ErrUnexpectedResponse without the body", body, err)
		}
	}
	// A strict field whose parse error would quote the value.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"points":"secretvalue"}`))
	})
	if _, err := c.Profile(t.Context()); !errors.Is(err, ErrUnexpectedResponse) || strings.Contains(err.Error(), "secretvalue") {
		t.Errorf("Profile error = %v, want ErrUnexpectedResponse without the body", err)
	}
}

func TestAZeroClientReturnsAnErrorInsteadOfPanicking(t *testing.T) {
	var zero Client
	if _, err := zero.Send(t.Context(), Message{To: "359888123456", Text: "hi"}); err == nil {
		t.Error("Send on a zero Client succeeded, want an error")
	}
	if _, err := zero.Profile(t.Context()); err == nil {
		t.Error("Profile on a zero Client succeeded, want an error")
	}
	var nilClient *Client
	if _, err := nilClient.Send(t.Context(), Message{To: "359888123456", Text: "hi"}); err == nil {
		t.Error("Send on a nil *Client succeeded, want an error")
	}
}

func TestTheDefaultClientDoesNotFollowRedirects(t *testing.T) {
	followed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/sms.do", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		followed = true
		_, _ = w.Write([]byte(queuedBody))
	})
	c := newTestClient(t, mux.ServeHTTP)

	_, err := c.Send(t.Context(), Message{To: "359888123456", Text: "hi"})
	if followed {
		t.Error("the client followed a redirect and replayed the request")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusTemporaryRedirect {
		t.Errorf("Send error = %v, want an *APIError with HTTP 307", err)
	}
}
