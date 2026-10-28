package smsapibg

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits smsapi.bg documents for sms.do, checked before a request is made.
const (
	maxSenderLen     = 11
	maxIdxLen        = 255
	maxParts         = 9
	maxNumberDigits  = 15 // E.164
	minExpiry        = 15 * time.Minute
	maxExpiry        = 72 * time.Hour
	sendPath         = "sms.do"
	formTrue         = "1"
	senderNameSymbol = "-. "
)

// Message is one SMS to one number. Only To and Text are required; every
// other field maps to an optional sms.do parameter and is sent only when set.
type Message struct {
	// To is the recipient with its country prefix, digits only, such as
	// 359888123456. A leading "+" or "00" is accepted and stripped. A national
	// number (starting with a single 0) is refused.
	To string
	// Text is the message body, UTF-8. smsapi.bg splits a long text into
	// parts and charges per part.
	Text string
	// From is a registered, active sender name: up to 11 characters of
	// a-z, A-Z, 0-9, "-", "." and space. Empty means the account default.
	From string
	// Test makes smsapi.bg validate the message and answer as if it were
	// sent, without sending or charging (test=1).
	Test bool
	// Normalize replaces special characters with plain equivalents
	// (normalize=1), which can keep a text in the cheaper GSM alphabet.
	Normalize bool
	// NoUnicode makes smsapi.bg refuse a text that needs unicode, with
	// CodeMessageInvalid, instead of sending it at the unicode price
	// (nounicode=1).
	NoUnicode bool
	// MaxParts caps how many parts the text may take, 1 to 9; 0 leaves the
	// account default (max_parts).
	MaxParts int
	// Idx is your own reference, up to 255 characters, echoed in delivery
	// reports (idx). It may not contain a comma, because delivery reports
	// join several values with commas.
	Idx string
	// CheckIdx makes smsapi.bg refuse, with CodeDuplicateIdx, a second
	// message with the same Idx within 24 hours (check_idx=1). It makes a
	// retry after an ambiguous failure safe. It requires Idx.
	CheckIdx bool
	// ExpiresAt is when an undelivered message stops being tried; zero
	// means the network default. It must be 15 minutes to 72 hours after
	// the send (expiration_date).
	ExpiresAt time.Time
	// NotifyURL receives this message's delivery reports instead of the
	// URL set in the customer portal (notify_url). See ParseDeliveryReport.
	NotifyURL string
	// Details asks smsapi.bg for more detail in the answer, such as the
	// number of parts (details=1), so Result.Parts is filled.
	Details bool
}

// Result is smsapi.bg's answer for a message it accepted.
type Result struct {
	// ID identifies the message in delivery reports (MsgId).
	ID string
	// Points is what the message cost.
	Points float64
	// Number is the recipient as smsapi.bg normalised it.
	Number string
	// Status is the initial status, usually "QUEUE".
	Status string
	// Parts is how many parts the text took; set when Message.Details is,
	// and 0 when smsapi.bg did not say.
	Parts int
	// SentAt is when smsapi.bg accepted the message; zero if not given.
	SentAt time.Time
}

// Send sends m (POST /sms.do). It checks m first and returns a
// *ValidationError, without calling the API, if a rule is broken.
//
// Retrying Send is safe only with Idx and CheckIdx set. Without them, a
// retry after an error that is ErrOutcomeUnknown (a timeout or a dropped
// connection after the request left, a 5xx without a code, 201, 999, an
// unreadable answer) can send the message twice. With them, a retry of a
// message that did go through fails with ErrDuplicateIdx: treat that as
// sent. The first attempt's message ID is then lost, so match its delivery
// reports on Idx. Stop retrying well inside the 24 h that check_idx
// remembers an Idx, and give a new message (a user's "resend code") a new
// Idx. Whether smsapi.bg applies check_idx to Test messages is not
// documented.
func (c *Client) Send(ctx context.Context, m Message) (Result, error) {
	if err := c.ready(); err != nil {
		return Result{}, err
	}
	form, err := m.form(c.now())
	if err != nil {
		return Result{}, err
	}
	// Only the ID is required; the other fields fail soft, so an odd value
	// in one of them cannot turn a message that was sent into an error.
	type item struct {
		ID     looseString `json:"id"`
		Points softFloat   `json:"points"`
		Number softString  `json:"number"`
		Date   softInt     `json:"date_sent"`
		Status softString  `json:"status"`
		Parts  softInt     `json:"parts"`
	}
	var body struct {
		List  []item  `json:"list"`
		Parts softInt `json:"parts"`
	}
	if err := c.call(ctx, http.MethodPost, sendPath, form, &body); err != nil {
		return Result{}, err
	}
	if len(body.List) == 0 {
		return Result{}, &unexpectedError{path: sendPath, reason: "no message in the answer"}
	}
	it := body.List[0]
	if it.ID == "" {
		return Result{}, &unexpectedError{path: sendPath, reason: "no message ID in the answer"}
	}
	res := Result{
		ID:     string(it.ID),
		Points: float64(it.Points),
		Number: string(it.Number),
		Status: string(it.Status),
		Parts:  int(it.Parts),
	}
	if res.Parts == 0 {
		res.Parts = int(body.Parts)
	}
	if it.Date != 0 {
		res.SentAt = time.Unix(int64(it.Date), 0)
	}
	return res, nil
}

// form checks m against the documented rules and builds the sms.do form.
// now is the moment ExpiresAt is measured from.
func (m Message) form(now time.Time) (url.Values, error) {
	to, err := normalizeNumber(m.To)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.Text) == "" {
		return nil, &ValidationError{Field: "message", Reason: "must not be empty"}
	}
	if !utf8.ValidString(m.Text) {
		return nil, &ValidationError{Field: "message", Reason: "must be valid UTF-8"}
	}
	f := url.Values{"to": {to}, "message": {m.Text}, "format": {"json"}}

	if m.From != "" {
		if err := checkSender(m.From); err != nil {
			return nil, err
		}
		f.Set("from", m.From)
	}
	setFlag(f, "test", m.Test)
	setFlag(f, "normalize", m.Normalize)
	setFlag(f, "nounicode", m.NoUnicode)
	setFlag(f, "details", m.Details)

	if m.MaxParts != 0 {
		if m.MaxParts < 1 || m.MaxParts > maxParts {
			return nil, &ValidationError{Field: "max_parts", Reason: "must be between 1 and 9"}
		}
		f.Set("max_parts", strconv.Itoa(m.MaxParts))
	}
	if err := m.idx(f); err != nil {
		return nil, err
	}
	if !m.ExpiresAt.IsZero() {
		if d := m.ExpiresAt.Sub(now); d < minExpiry || d > maxExpiry {
			return nil, &ValidationError{Field: "expiration_date", Reason: "must be 15 minutes to 72 hours from now"}
		}
		f.Set("expiration_date", strconv.FormatInt(m.ExpiresAt.Unix(), 10))
	}
	if m.NotifyURL != "" {
		u, err := url.Parse(m.NotifyURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, &ValidationError{Field: "notify_url", Reason: "must be an absolute http or https URL"}
		}
		f.Set("notify_url", m.NotifyURL)
	}
	return f, nil
}

func (m Message) idx(f url.Values) error {
	if m.Idx == "" {
		if m.CheckIdx {
			return &ValidationError{Field: "idx", Reason: "is required with CheckIdx"}
		}
		return nil
	}
	if utf8.RuneCountInString(m.Idx) > maxIdxLen {
		return &ValidationError{Field: "idx", Reason: "must be at most 255 characters"}
	}
	if strings.ContainsFunc(m.Idx, func(r rune) bool { return r == ',' || unicode.IsControl(r) }) {
		return &ValidationError{Field: "idx", Reason: "must not contain a comma or control characters"}
	}
	f.Set("idx", m.Idx)
	setFlag(f, "check_idx", m.CheckIdx)
	return nil
}

// normalizeNumber strips a leading "+" or "00" and checks what is left is
// an international number: 1 to 15 digits, not starting with 0.
func normalizeNumber(raw string) (string, error) {
	n := raw
	switch {
	case strings.HasPrefix(n, "+"):
		n = n[1:]
	case strings.HasPrefix(n, "00"):
		n = n[2:]
	}
	switch {
	case n == "":
		return "", &ValidationError{Field: "to", Reason: "must not be empty"}
	case strings.ContainsFunc(n, func(r rune) bool { return r < '0' || r > '9' }):
		return "", &ValidationError{Field: "to", Reason: "must contain digits only, after an optional + or 00"}
	case n[0] == '0':
		return "", &ValidationError{Field: "to", Reason: "must start with a country prefix, not a national 0"}
	case len(n) > maxNumberDigits:
		return "", &ValidationError{Field: "to", Reason: "must be at most 15 digits"}
	}
	return n, nil
}

func checkSender(from string) error {
	if len(from) > maxSenderLen {
		return &ValidationError{Field: "from", Reason: "must be at most 11 characters"}
	}
	for _, r := range from {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(senderNameSymbol, r)
		if !ok {
			return &ValidationError{Field: "from", Reason: "may contain only a-z, A-Z, 0-9, '-', '.' and space"}
		}
	}
	return nil
}

func setFlag(f url.Values, name string, on bool) {
	if on {
		f.Set(name, formTrue)
	}
}
