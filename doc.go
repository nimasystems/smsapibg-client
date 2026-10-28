// Package smsapibg is a small client for the smsapi.bg SMS API
// (https://www.smsapi.bg/docs/).
//
// It covers what a backend needs to send one transactional SMS, such as a
// phone-verification code, and to follow it: [Client.Send] sends one message
// to one number, [Client.Profile] reads the account balance, and
// [ParseDeliveryReport] reads the delivery-report callback that smsapi.bg
// makes to a URL you configure. The rest of the API (bulk and scheduled
// sending, templates, MMS, VMS, MFA codes, contacts, sub-users, sender-name
// management, short links, HLR) is not covered yet; the README keeps the
// list.
//
// A [Client] is safe for concurrent use. It holds the API token behind an
// unexported pointer and never prints it: not in errors, not through fmt
// with any verb (%p and %d included), not through log/slog or encoding/json.
// It sends the token only in the Authorization header, and only over HTTPS
// (plain HTTP is allowed for a loopback base URL so tests can use
// net/http/httptest).
//
// Errors from the API are [*APIError] values. Callers branch on a class with
// errors.Is ([ErrInsufficientCredits], [ErrUnauthorized], [ErrInvalidNumber],
// [ErrRateLimited], [ErrBlocked], [ErrTemporary]) or on the exact code with
// errors.As. A message refused before any request is made is a
// [*ValidationError].
//
// The package uses the standard library only.
package smsapibg
