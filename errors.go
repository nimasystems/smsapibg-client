package smsapibg

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Error codes smsapi.bg returns in the "error" field of sms.do and other
// endpoints. The names follow the API documentation; the list is not
// exhaustive (codes for sub-users, contacts and sender names are left out
// until those endpoints are covered). Compare against [APIError.Code].
const (
	CodeRequestError            = 8   // error in the request; smsapi.bg asks you to report it
	CodeMessageInvalid          = 11  // text too long, empty, or special characters with nounicode
	CodeTooManyParts            = 12  // more parts than max_parts allows
	CodeNoValidNumbers          = 13  // no valid number (invalid or blacklisted)
	CodeWrongSenderName         = 14  // sender name not registered or not active
	CodeParameterCountMismatch  = 18  // invalid number of parameters
	CodeIdxCountMismatch        = 20  // invalid number of idx parameters
	CodeIdxTooLong              = 27  // idx longer than 255 characters
	CodeScheduleTooFar          = 50  // scheduled more than 3 months ahead
	CodeTooManyAttemptsToNumber = 52  // more than 10 attempts to one number within 60 s
	CodeDuplicateIdx            = 53  // idx already used within 24 h, with check_idx=1
	CodeWrongDate               = 54  // date is neither unix time nor ISO 8601
	CodeExpirationOutOfRange    = 56  // expiration_date too close or too far
	CodeBlacklisted             = 57  // the number is on the account's blacklist
	CodeOptedOut                = 59  // the number is on the opt-out list
	CodeBadNotifyURL            = 70  // notify_url is not a valid URL
	CodeDateRestricted          = 74  // send date outside the account's allowed hours
	CodeInvalidCharacters       = 76  // invalid characters in parameters
	CodeLinksNotAllowed         = 94  // the account may not send links
	CodeCountryVolumeReached    = 96  // the account's volume for that country is used up
	CodeRegistrationNumberOnly  = 98  // the account may send only to its registration number
	CodeBadAuthorization        = 101 // invalid authorization
	CodeBadCredentials          = 102 // invalid username or password
	CodeInsufficientCredits     = 103 // not enough points on the account
	CodeIPNotAllowed            = 105 // the caller's IP is not on the account's allow-list
	CodeActionNotAllowed        = 110 // the account may not do this
	CodeCountryRestricted       = 112 // the account may not send to that country
	CodeSubmissionFailed        = 200 // the submission failed
	CodeInternalError           = 201 // smsapi.bg internal error
	CodeTooManySimultaneous     = 202 // too many simultaneous requests; nothing was sent
	CodeTooManyRequests         = 203 // too many requests; retry later
	CodeTokenLacksPermission    = 401 // the token lacks the scope for this action
	CodeHTTPSRequired           = 997 // plain HTTP is disabled for the account
	CodeSystemError             = 999 // smsapi.bg internal error
)

// Classes of failure a caller usually branches on. Test with errors.Is; an
// [*APIError] matches every class its code belongs to (202 is both
// ErrRateLimited and ErrTemporary).
var (
	// ErrInsufficientCredits means the account has run out of points (103).
	ErrInsufficientCredits = errors.New("smsapibg: insufficient credits")
	// ErrUnauthorized means the token is wrong or lacks the scope (101, 102,
	// 401, an "authorization_failed" error, or HTTP 401).
	ErrUnauthorized = errors.New("smsapibg: unauthorized")
	// ErrInvalidNumber means the recipient number was refused, by smsapi.bg
	// (13, or a list of invalid numbers without another code) or locally
	// before sending.
	ErrInvalidNumber = errors.New("smsapibg: invalid number")
	// ErrRateLimited means too many requests, overall (202, 203, HTTP 429)
	// or to one number (52).
	ErrRateLimited = errors.New("smsapibg: rate limited")
	// ErrBlocked means the recipient is blacklisted (57) or opted out (59);
	// retrying will not help.
	ErrBlocked = errors.New("smsapibg: recipient blocked")
	// ErrTemporary means a later retry may succeed (8, 200, 201, 202, 203,
	// 999, or an HTTP 429 or 5xx without a code). See [APIError.Temporary].
	// It does not mean a retry is safe: some of these are also
	// ErrOutcomeUnknown, and retrying Send is safe only with Idx and
	// CheckIdx set.
	ErrTemporary = errors.New("smsapibg: temporary failure")
	// ErrDuplicateIdx means a message with this Idx was already accepted in
	// the last 24 h (53, with CheckIdx): treat it as sent. On a retry it is
	// the answer that proves the first attempt went through.
	ErrDuplicateIdx = errors.New("smsapibg: duplicate idx")

	// ErrOutcomeUnknown means the request may have reached smsapi.bg and
	// no clear answer came back, so a Send may or may not have gone out: a
	// transport error or timeout after the request was written, a 5xx
	// without a code, 201 or 999, or a 2xx that could not be read. A
	// failure before the request left (a refused connection, a context
	// cancelled first) is not this. Retry a Send only with Idx and CheckIdx.
	ErrOutcomeUnknown = errors.New("smsapibg: outcome unknown")

	// ErrUnexpectedResponse means a successful HTTP answer that this
	// library could not read. On Send the message may have been accepted.
	ErrUnexpectedResponse = errors.New("smsapibg: unexpected response")
)

// APIError is an error smsapi.bg reported. It is returned whether the
// error came with HTTP 200 or with another status.
type APIError struct {
	// Code is the numeric error code, one of the Code constants; 0 when the
	// API gave a string Name instead, or no body at all.
	Code int
	// Name is the string error identifier some endpoints use instead of a
	// code, such as "authorization_failed".
	Name string
	// Message is smsapi.bg's description of the error.
	Message string
	// InvalidNumbers lists the numbers smsapi.bg refused. They are personal
	// data: do not log them as they are.
	InvalidNumbers []InvalidNumber
	// HTTPStatus is the status of the response that carried the error.
	HTTPStatus int
}

// InvalidNumber is one refused recipient in an [APIError].
type InvalidNumber struct {
	Number          string `json:"number"`
	SubmittedNumber string `json:"submitted_number"`
	Message         string `json:"message"`
}

// Error names the code and smsapi.bg's message. It leaves out the invalid
// numbers, so the error can be logged without personal data.
func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("smsapibg: api error")
	switch {
	case e.Code != 0:
		b.WriteString(" " + strconv.Itoa(e.Code))
	case e.Name != "":
		b.WriteString(" " + e.Name)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.HTTPStatus != 0 {
		b.WriteString(" (http " + strconv.Itoa(e.HTTPStatus) + ")")
	}
	return b.String()
}

// Is matches e against the class sentinels, so callers can write
// errors.Is(err, smsapibg.ErrInsufficientCredits).
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrInsufficientCredits:
		return e.Code == CodeInsufficientCredits
	case ErrUnauthorized:
		switch e.Code {
		case CodeBadAuthorization, CodeBadCredentials, CodeTokenLacksPermission:
			return true
		}
		return e.Name == "authorization_failed" || (e.Code == 0 && e.HTTPStatus == http.StatusUnauthorized)
	case ErrInvalidNumber:
		// A list of numbers also comes with other codes (53 lists the
		// duplicate, 57 the blacklisted); only 13, or a list with no code,
		// means the number itself is bad.
		return e.Code == CodeNoValidNumbers || (e.Code == 0 && len(e.InvalidNumbers) > 0)
	case ErrDuplicateIdx:
		return e.Code == CodeDuplicateIdx
	case ErrRateLimited:
		switch e.Code {
		case CodeTooManyAttemptsToNumber, CodeTooManySimultaneous, CodeTooManyRequests:
			return true
		}
		return e.Code == 0 && e.HTTPStatus == http.StatusTooManyRequests
	case ErrBlocked:
		return e.Code == CodeBlacklisted || e.Code == CodeOptedOut
	case ErrTemporary:
		return e.Temporary()
	case ErrOutcomeUnknown:
		switch e.Code {
		case CodeInternalError, CodeSystemError:
			return true
		case 0:
			return e.Name == "" && e.HTTPStatus >= 500
		}
	}
	return false
}

// Temporary reports whether retrying the same request later may succeed. A
// refusal that depends on the request or the account (credits, numbers,
// sender name) is not temporary; a 52 (too many attempts to one number) is
// not either, because retrying soon makes it worse.
func (e *APIError) Temporary() bool {
	switch e.Code {
	case CodeRequestError, CodeSubmissionFailed, CodeInternalError,
		CodeTooManySimultaneous, CodeTooManyRequests, CodeSystemError:
		return true
	case 0:
		return e.Name == "" && (e.HTTPStatus == http.StatusTooManyRequests || e.HTTPStatus >= 500)
	}
	return false
}

// ValidationError is a message refused locally, before any request was
// made. It names the API parameter and the rule, never the value, so it is
// safe to log.
type ValidationError struct {
	// Field is the API parameter name, such as "to", "message" or "from".
	Field string
	// Reason says which rule the value broke.
	Reason string
}

func (e *ValidationError) Error() string {
	return "smsapibg: invalid " + e.Field + ": " + e.Reason
}

// Is makes a bad recipient number match [ErrInvalidNumber], so a caller
// handles it the same way whether it was caught locally or by smsapi.bg.
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidNumber && e.Field == "to"
}
