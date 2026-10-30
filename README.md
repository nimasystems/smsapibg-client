# smsapibg-client

A small Go client for the [smsapi.bg](https://www.smsapi.bg/) SMS API: send
one SMS, read the account balance, and parse delivery reports.

It was written for sending phone-verification codes from a Go backend, and
it covers that job well rather than the whole API. It is built to grow: each
new endpoint is added the same way, with tests (see [Contributing](#contributing)).

- Standard library only: no dependencies.
- Go 1.24 or later: the oldest release it needs (its tests use
  `testing.T.Context`, new in 1.24). CI tests 1.24 and the latest stable Go.
- Safe for concurrent use.
- The API token never appears in errors, in `fmt` output with any verb, or
  in `log/slog`.

> **Not affiliated with smsapi.bg.** This is an independent, open-source
> client. "smsapi.bg" is named only to say which API it talks to.

## Install

```sh
go get github.com/nimasystems/smsapibg-client@latest
```

```go
import smsapibg "github.com/nimasystems/smsapibg-client"
```

## Quick start

```go
client, err := smsapibg.New(os.Getenv("SMSAPIBG_TOKEN"),
	smsapibg.WithUserAgent("my-app/1.0"),
)
if err != nil {
	return err
}

res, err := client.Send(ctx, smsapibg.Message{
	To:   "+359888123456",
	Text: "Your code is 123456",
})
if err != nil {
	return err
}
log.Printf("queued message %s (%.2f points)", res.ID, res.Points)
```

Get the token from the smsapi.bg customer portal and give it only the
scopes you use: sending SMS, and reading the profile if you check the
balance. Keep it in your secret store, not in code.

## Configuration

`New(token, opts...)` returns a `*Client`. It refuses an empty token, a
token with whitespace, and an invalid option. Always make a client with
`New`: the methods of a zero `Client{}` return an error.

| Option | Default | Notes |
|---|---|---|
| `WithBaseURL(url)` | `https://api.smsapi.bg/` | Must be `https`; plain `http` only on a loopback host, for tests. Must not carry `user:password@`. A path prefix is kept (for a proxy). |
| `WithHTTPClient(hc)` | a private `*http.Client` with a 15 s timeout (`DefaultTimeout`), TLS 1.2 or later, and no redirects (a 3xx comes back as an `*APIError`; following it would replay the POST) | Used as given: if its `Timeout` is zero, only your context bounds a call, and it follows redirects unless you set its `CheckRedirect`. A transport that hides dial, DNS or TLS errors in its own types makes more errors `ErrOutcomeUnknown`. |
| `WithUserAgent(ua)` | `smsapibg-client-go` (`DefaultUserAgent`) | Name your application so smsapi.bg can tell its traffic apart. |

Every call takes a `context.Context`; cancel it or give it a deadline to
stop a request early.

## Sending

`(*Client).Send(ctx, Message) (Result, error)` posts one message to one
number to `sms.do` with `format=json`.

| `Message` field | API parameter | Rule checked before sending |
|---|---|---|
| `To` | `to` | Required. Country prefix and number, digits only, at most 15 (`359888123456`). A leading `+` or `00` is stripped. A national number (`0888...`) is refused. |
| `Text` | `message` | Required, not blank, valid UTF-8. |
| `From` | `from` | Optional sender name: up to 11 characters of `a-z A-Z 0-9 - .` and space. It must be registered and active in your account. Empty means the account default. |
| `Test` | `test=1` | smsapi.bg validates and answers, but sends and charges nothing. |
| `Normalize` | `normalize=1` | Replaces special characters with plain ones. |
| `NoUnicode` | `nounicode=1` | Refuses a text that needs unicode (error 11) instead of sending it at the unicode price. |
| `MaxParts` | `max_parts` | 1 to 9; 0 leaves the account default. |
| `Idx` | `idx` | Your reference, echoed in delivery reports. At most 255 characters, with no comma (delivery reports join values with commas) and no control characters. |
| `CheckIdx` | `check_idx=1` | Refuses a second message with the same `Idx` within 24 h (error 53). Needs `Idx`. |
| `ExpiresAt` | `expiration_date` | Unix time. 15 minutes to 72 hours from now, if set. |
| `NotifyURL` | `notify_url` | An absolute `http`/`https` URL for this message's delivery reports. |
| `Details` | `details=1` | Asks for more detail, so `Result.Parts` is filled. |

A message that breaks a rule returns a `*ValidationError` and no request is
made. The local rules are the documented ones plus a few that catch
mistakes early (a national number, more than 15 digits, a comma in `Idx`, a
blank text); smsapi.bg still has the last word. One known gap in its docs: the `expiration_date`
parameter allows 15 minutes to 72 hours, while the text of error 56 says
1 to 72 hours. The client allows the wider window; if smsapi.bg disagrees,
`Send` returns `CodeExpirationOutOfRange` (56).

`Result` holds `ID` (the message ID that delivery reports carry), `Points`
(the cost), `Number` (as smsapi.bg normalised it), `Status` (usually
`QUEUE`), `Parts` (with `Details`), `SentAt` and `Raw`, smsapi.bg's answer
exactly as received (a copy), for a caller that keeps it to diagnose a delivery
later. `Raw` holds the recipient's number: it is personal data, so do not log it
as it is.

### Retries without double sends

**Retrying `Send` is safe only with `Idx` and `CheckIdx` set.** A timeout, a
dropped connection after the request left, a 5xx with no error code, error 201
or 999, or an unreadable answer leaves it unknown whether the message went
out: these errors are `errors.Is(err, smsapibg.ErrOutcomeUnknown)`. A retry
without `CheckIdx` can then send the SMS twice.

With them:

- Give every message an `Idx` (a verification attempt ID, say) and set
  `CheckIdx`. If the first attempt did go through, the retry fails with
  `ErrDuplicateIdx` (53) instead of sending a second SMS. **Treat
  `ErrDuplicateIdx` as sent.**
- The first attempt's message ID is lost then (53 does not return it), so
  match delivery reports on `Idx`, not on `Result.ID`.
- smsapi.bg remembers an `Idx` for 24 hours. Stop retrying well inside that
  (minutes, not hours), or a late retry sends again.
- A new message needs a new `Idx`. When a user asks to resend a code, that
  is a new attempt: reusing the old `Idx` would block it with 53 for 24 h.
- Whether smsapi.bg applies `check_idx` to `Test: true` messages is not
  documented, so do not rely on test mode to try this out.

Which errors to retry, with backoff: `ErrOutcomeUnknown` and `ErrTemporary`
(only with `CheckIdx`), and transport errors that are not
`ErrOutcomeUnknown`. A transport error is `ErrOutcomeUnknown` unless it is
clearly from before sending: a refused or failed dial, a DNS failure, a
failed TLS handshake or certificate check, or a context that was done
before the call. Those never reached smsapi.bg, so they can be retried
without `CheckIdx`. Anything else, including any error a custom transport
(`WithHTTPClient`) makes up, counts as unknown. Do not retry
`ErrRateLimited` for code 52 (too many messages to one number) soon: it
only makes it worse.

## Errors

| Error | What it means | How to test |
|---|---|---|
| `*APIError` | smsapi.bg refused the request. Holds `Code`, `Name` (a string ID some endpoints use, such as `authorization_failed`), `Message`, `InvalidNumbers`, `HTTPStatus` and `Raw` (the error body as received; nil when there was none or a non-2xx body was not JSON). It is returned whether the error came with HTTP 200 or another status. | `errors.As(err, &apiErr)` |
| `ErrInsufficientCredits` | Out of points (103). | `errors.Is` |
| `ErrUnauthorized` | Bad token or missing scope (101, 102, 401, `authorization_failed`, HTTP 401). | `errors.Is` |
| `ErrInvalidNumber` | The number was refused, by smsapi.bg (13, or a list of invalid numbers without another code) or locally (`ValidationError` on `to`). Another code that comes with a list of numbers (53, 57, ...) is not this. | `errors.Is` |
| `ErrDuplicateIdx` | A message with this `Idx` was already accepted in the last 24 h (53, with `CheckIdx`): treat it as sent. | `errors.Is` |
| `ErrRateLimited` | Too many requests overall (202, 203, HTTP 429) or to one number (52). | `errors.Is` |
| `ErrBlocked` | The recipient is blacklisted (57) or opted out (59). Do not retry. | `errors.Is` |
| `ErrTemporary` | A retry may succeed (8, 200, 201, 202, 203, 999, or HTTP 429 or 5xx without a code). Same as `apiErr.Temporary()`. It does not say a retry is safe: see [Retries](#retries-without-double-sends). | `errors.Is` |
| `*ValidationError` | A message refused locally; names the parameter (`Field`) and the rule, never the value. | `errors.As` |
| `ErrOutcomeUnknown` | The request may have reached smsapi.bg and no clear answer came back: a transport error that is not clearly from before sending (a timeout, a dropped connection), a 5xx with no error code (numeric or named), 201, 999, or an unreadable 2xx. A `Send` may or may not have gone out; see [Retries](#retries-without-double-sends). | `errors.Is` |
| `ErrUnexpectedResponse` | A 2xx answer that could not be read (also `ErrOutcomeUnknown`). | `errors.Is` |
| `ErrMalformedReport` | A delivery callback with missing or misaligned parameters. | `errors.Is` |

The `sms.do` and authorization error codes have named constants
(`CodeNoValidNumbers`, `CodeWrongSenderName`, `CodeDuplicateIdx`, ...) for
when you need the exact code; codes of uncovered areas (sub-users, contacts,
sender names) are not named yet but still arrive in `APIError.Code`. A transport failure (DNS, TLS, timeout, cancelled context) is returned
wrapped: `errors.Is(err, context.Canceled)` and
`errors.Is(err, context.DeadlineExceeded)` work.

Error messages leave out the token, phone numbers and message text, so they
are safe to log. `APIError.InvalidNumbers`, `APIError.Raw`, `Result.Raw` and
`Profile` hold personal data: do not log those as they are.

## Balance

```go
p, err := client.Profile(ctx) // GET /profile
if err != nil {
	return err
}
if p.Points < 50 {
	alert("smsapi.bg balance is low")
}
```

## Delivery reports

smsapi.bg calls a URL you set in the customer portal (or per message with
`NotifyURL`) with `GET` and the report in the query. One call can carry
several reports, each parameter a comma-separated list. The handler is
yours; `ParseDeliveryReport` reads the query:

```go
http.HandleFunc("/sms/dlr/{secret}", func(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.PathValue("secret")), []byte(dlrSecret)) != 1 {
		http.NotFound(w, r)
		return
	}
	reports, err := smsapibg.ParseDeliveryReport(r.URL.Query())
	if err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	for _, rep := range reports {
		// rep.MessageID, rep.Idx, rep.Status, rep.Status.Final(), rep.Status.Delivered(), rep.DoneAt
		store(rep) // a report you have already stored is a no-op
	}
	io.WriteString(w, "OK") // exactly "OK", or smsapi.bg sends the reports again
})
```

- Answer with the body `OK` once the reports are stored; anything else makes
  smsapi.bg resend them, so storing must be idempotent.
- The callback is not signed. Put a secret in the URL path, or allow only
  smsapi.bg's addresses, if forged reports matter.
- `DeliveryStatus` names each code (`StatusDelivered` is 404 and prints
  `DELIVERED`). `Final()` is true when no further report should come
  (`DELIVERED`, `UNDELIVERED`, `EXPIRED`, `FAILED`, `REJECTED`, `UNKNOWN`,
  `NOT_FOUND`, `STOP`), and `Delivered()` only for `DELIVERED`. A code this
  library does not know is kept as a number and is not final.
- `SENT` (403, "sent without a final delivery report") is deliberately not
  final: where the network reports delivery, `DELIVERED` or `UNDELIVERED`
  follows it, and a final `SENT` would hide that. Where the network does not,
  nothing follows, so never wait on `Final()` alone: stop after a deadline of
  your own and treat a last `SENT` as probably delivered.
- The report fields read are `MsgId`, `status`, `status_name`, `to`, `idx`
  and `donedate`. The rest (`sent_at`, `points`, `username`, `mcc`, `mnc`,
  `from`) are left in the query for you.

## Testing your code

- **Against smsapi.bg without sending:** set `Test: true`. smsapi.bg checks
  the message (number, sender name, length) and answers as if it sent it,
  with nothing sent and nothing charged.
- **Without a network:** point the client at an `httptest.Server` with
  `WithBaseURL(srv.URL)`; plain `http` is accepted for loopback addresses.
  `example_test.go` does exactly that.

## Coverage of the smsapi.bg API

What this version covers, and what it does not yet. This table is kept
true: a change that adds or drops a feature updates it.

Areas follow the sections of the [smsapi.bg API docs](https://www.smsapi.bg/docs/).

| Area (docs section) | API | Status |
|---|---|---|
| Single SMS, to one number | `POST /sms.do` | Covered: `to`, `message`, `from`, `test`, `normalize`, `nounicode`, `max_parts`, `idx`, `check_idx`, `expiration_date`, `notify_url`, `details`, `format=json` |
| Message encoding | `encoding` | UTF-8 only (the API default); other encodings not yet |
| Scheduled SMS | `date`, `date_validate`, `sch_del` | Not yet |
| Other single-SMS flags | `allow_duplicates` and the rest of the parameter table | Not yet |
| Bulk SMS: many numbers, groups, personalised messages, opt-out, discount codes, cutme.bg short links | `sms.do` with lists, `group`, ... | Not yet |
| SMS templates | `template`, `param1..4`, `single` | Not yet |
| Account balance | `GET /profile` | Covered |
| Callback reports: SMS delivery reports | `GET` to your URL | Covered: `MsgId`, `status`, `status_name`, `to`, `idx`, `donedate` |
| Callback reports: bulk and cutme.bg callbacks | `GET` to your URL | Not yet |
| Receiving messages (inbound SMS) | callback | Not yet |
| Contact database (contacts, groups) | `/contacts` | Not yet |
| Black list | `/blacklist/phone_numbers` | Not yet |
| User accounts administration (sub-users) | `/subusers` | Not yet |
| Sender names | | Not yet |
| Price list | | Not yet |
| SMS Authenticator (MFA codes) | | Not yet (send your own code with `Send` instead) |
| MMS, VMS, HLR | | Not covered (not in the smsapi.bg docs this client was written against) |
| Text (non-JSON) responses | `format` other than `json` | Not planned: the client always asks for JSON |

## Versioning

The module follows [semantic versioning](https://semver.org/). It stays at
`v0` while the API settles, so a minor release may still change it; each
release says what changed. `v1` will follow once the shape has been proven
in production.

## Contributing

Issues and pull requests are welcome. The rules for a change are in
[CLAUDE.md](CLAUDE.md) (they bind people and coding agents alike): test
first, standard library only, no secrets or personal data in errors or
logs, and keep the coverage table above true. Run `make check` before you
push: it is what CI runs (gofmt, `go vet`, staticcheck, govulncheck,
`go test -race` and a 90% coverage floor). Dependabot proposes updates to the
pinned GitHub Actions weekly.

## License

[MIT](LICENSE).
