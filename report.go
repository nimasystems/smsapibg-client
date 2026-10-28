package smsapibg

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DeliveryStatus is the numeric status in a delivery report.
type DeliveryStatus int

// Delivery statuses smsapi.bg reports.
const (
	StatusNotFound    DeliveryStatus = 401 // wrong message ID, or the report has expired
	StatusExpired     DeliveryStatus = 402 // the message expired before delivery
	StatusSent        DeliveryStatus = 403 // sent; no final report yet
	StatusDelivered   DeliveryStatus = 404 // delivered to the phone
	StatusUndelivered DeliveryStatus = 405 // not delivered (invalid number, roaming error, ...)
	StatusFailed      DeliveryStatus = 406 // sending failed; smsapi.bg asks you to report it
	StatusRejected    DeliveryStatus = 407 // not delivered, for the same causes as 405
	StatusUnknown     DeliveryStatus = 408 // no report came; delivery is not known
	StatusQueue       DeliveryStatus = 409 // waiting to be sent
	StatusAccepted    DeliveryStatus = 410 // handed to the operator
	StatusStop        DeliveryStatus = 412 // the bulk was stopped by the user
)

var statusNames = map[DeliveryStatus]string{
	StatusNotFound:    "NOT_FOUND",
	StatusExpired:     "EXPIRED",
	StatusSent:        "SENT",
	StatusDelivered:   "DELIVERED",
	StatusUndelivered: "UNDELIVERED",
	StatusFailed:      "FAILED",
	StatusRejected:    "REJECTED",
	StatusUnknown:     "UNKNOWN",
	StatusQueue:       "QUEUE",
	StatusAccepted:    "ACCEPTED",
	StatusStop:        "STOP",
}

// String returns the status name smsapi.bg uses, such as "DELIVERED".
func (s DeliveryStatus) String() string {
	if name, ok := statusNames[s]; ok {
		return name
	}
	return "DeliveryStatus(" + strconv.Itoa(int(s)) + ")"
}

// Final reports whether no further report should be expected for the
// message: it was delivered, or it will not be. QUEUE and ACCEPTED are on
// the way; an unknown code is not treated as final.
//
// SENT (403, "sent without a final delivery report") is not final either.
// On a network that returns delivery reports, DELIVERED or UNDELIVERED
// follows it, and calling SENT final would drop that outcome. On a network
// that does not, nothing follows: so do not wait for Final alone; give up
// after a deadline of your own (ExpiresAt plus a margin, say) and treat a
// last SENT as "probably delivered".
func (s DeliveryStatus) Final() bool {
	switch s {
	case StatusNotFound, StatusExpired, StatusDelivered, StatusUndelivered,
		StatusFailed, StatusRejected, StatusUnknown, StatusStop:
		return true
	}
	return false
}

// Delivered reports whether the message reached the phone.
func (s DeliveryStatus) Delivered() bool { return s == StatusDelivered }

// DeliveryReport is one message's report from a delivery callback.
type DeliveryReport struct {
	// MessageID is the Result.ID of the message (MsgId).
	MessageID string
	// Status is the numeric status (status).
	Status DeliveryStatus
	// StatusName is smsapi.bg's name for it (status_name), kept as sent so a
	// status this library does not know is still readable.
	StatusName string
	// To is the recipient number (to). It is personal data.
	To string
	// Idx is the Message.Idx the message was sent with (idx).
	Idx string
	// DoneAt is when the status was reached (donedate); zero if not given.
	DoneAt time.Time
}

// ErrMalformedReport means a delivery callback is missing a parameter or
// its comma-separated lists do not line up.
var ErrMalformedReport = errors.New("smsapibg: malformed delivery report")

// ParseDeliveryReport reads the query of a delivery-report callback, which
// smsapi.bg makes with GET to the URL set in the customer portal or in
// Message.NotifyURL. One callback can carry several reports, each parameter
// a comma-separated list of the same length.
//
// The HTTP handler is yours: pass it r.URL.Query(), and answer with the
// body "OK" (exactly) once the reports are stored, or smsapi.bg will send
// them again. Because of that, handle a report you have already seen as a
// no-op. The callback carries no signature: put a secret in the callback URL
// path or restrict it by source IP if forged reports matter to you.
//
// Errors wrap ErrMalformedReport and name the parameter, never its values.
func ParseDeliveryReport(q url.Values) ([]DeliveryReport, error) {
	ids, err := required(q, "MsgId")
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("%w: an empty MsgId", ErrMalformedReport)
		}
	}
	n := len(ids)
	statuses, err := required(q, "status")
	if err != nil {
		return nil, err
	}
	if len(statuses) != n {
		return nil, mismatch("status")
	}
	names, err := optional(q, "status_name", n)
	if err != nil {
		return nil, err
	}
	to, err := optional(q, "to", n)
	if err != nil {
		return nil, err
	}
	idx, err := optional(q, "idx", n)
	if err != nil {
		return nil, err
	}
	done, err := optional(q, "donedate", n)
	if err != nil {
		return nil, err
	}

	reports := make([]DeliveryReport, n)
	for i := range reports {
		code, err := strconv.Atoi(statuses[i])
		if err != nil {
			return nil, fmt.Errorf("%w: status is not a number", ErrMalformedReport)
		}
		r := DeliveryReport{
			MessageID:  ids[i],
			Status:     DeliveryStatus(code),
			StatusName: names[i],
			To:         to[i],
			Idx:        idx[i],
		}
		if done[i] != "" {
			sec, err := strconv.ParseInt(done[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: donedate is not a unix time", ErrMalformedReport)
			}
			r.DoneAt = time.Unix(sec, 0)
		}
		reports[i] = r
	}
	return reports, nil
}

func required(q url.Values, name string) ([]string, error) {
	if !q.Has(name) {
		return nil, fmt.Errorf("%w: no %s", ErrMalformedReport, name)
	}
	return strings.Split(q.Get(name), ","), nil
}

// optional splits a parameter that may be absent; absent, it is n empty
// values.
func optional(q url.Values, name string, n int) ([]string, error) {
	if !q.Has(name) {
		return make([]string, n), nil
	}
	v := strings.Split(q.Get(name), ",")
	if len(v) != n {
		return nil, mismatch(name)
	}
	return v, nil
}

func mismatch(name string) error {
	return fmt.Errorf("%w: %s has a different number of values than MsgId", ErrMalformedReport, name)
}
