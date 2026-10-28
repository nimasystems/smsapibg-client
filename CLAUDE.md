# CLAUDE.md: rules for every change

These rules bind people and coding agents alike. The README says what the
library does; this file says how to change it.

## The gate

- `make check` is the gate: gofmt, `go vet`, staticcheck, `go test -race`
  and a 90% coverage floor (`make cover`). CI (`.github/workflows/ci.yml`)
  runs the same targets on Go 1.26. Run `make check` before you push. A red
  gate is never merged.
- Never weaken the gate to make a change pass: no lowering the coverage
  floor, no skipping a check, no `//nolint`-style escapes. Fix the change.
- Every Makefile target has a `## description` (`make help` lists them).

## Tests first

- TDD: write the failing test, watch it fail for the right reason, make it
  pass, then refactor. A check you never saw fail is not trusted. When a test
  passes at once, break the code on purpose to see it fail.
- Standard `testing` only. No assertion libraries, no mock generators. Fakes
  are `httptest` servers that assert the exact method, path, headers and form
  fields.
- Name tests as sentences: `TestASendPostsTheFormWithTheBearerToken`.
- Cover the error paths of every endpoint: an error body with HTTP 200, a
  non-2xx with and without a JSON body, malformed JSON, context cancel and
  timeout.
- No real network in tests, and no real token anywhere: tests use the
  placeholder `testToken`.
- Inject the clock: code that compares against "now" uses `Client.now`, never
  `time.Now` directly, so tests run on a fixed time.

## Dependencies

- Standard library only. A new dependency needs a reason in the pull
  request (what it does that the standard library cannot do reasonably) and
  the owner's agreement. Tools run through `go run pkg@version` in the
  Makefile do not count, because they never enter `go.mod`.

## Security and personal data

- Never log, print or put in an error the API token. It lives behind an
  unexported pointer (`token *secret`): `fmt` prints a nested pointer as an
  address, so even a verb `String` does not serve (`%d`, `%p` of a `Client`
  value) cannot reach it. `secret` and `Client` also redact through `Format`,
  `String`, `GoString`, `LogValue` and `MarshalText`.
  `TestTheClientNeverPrintsItsTokenWithAnyVerb` checks every verb; extend it
  when you add a field. `Client` has no exported token field. Keep it that
  way. Only `call` reveals the token, for the `Authorization` header. It
  never goes in a URL or a query string.
- Errors never echo phone numbers, message text, sender names, URLs with
  credentials or raw response bodies. Name the parameter and the rule
  instead (`ValidationError{Field, Reason}`).
- Values that are personal data (`InvalidNumbers`, `Profile`, `DeliveryReport.To`)
  are documented as such where they are declared.
- The base URL is `https`, or `http` only on loopback for tests, and never
  carries userinfo.

## Adding an endpoint

1. Read the endpoint in https://www.smsapi.bg/docs/ and note any doubt
   (an undocumented field, a conflicting limit) in the pull request and in
   the README.
2. Write the tests first, in `<feature>_test.go`: the exact request (method,
   path, headers, form or query), the success answer, and the error paths
   above.
3. Implement in `<feature>.go` as a method on `*Client` that takes a
   `context.Context` and goes through `c.call`, so auth, limits and error
   decoding stay in one place. Validate input locally with
   `*ValidationError` where the docs give a rule.
4. Decode numbers with the `loose*` types when a misread would turn a
   success into an error (for example, a sent message reported as failed).
5. Add any new error codes as `Code*` constants, and class them in
   `APIError.Is`/`Temporary` if callers will branch on them.
6. Document every exported identifier, saying why it exists, not what it
   is. Add or extend an `Example` in `example_test.go` when usage is not
   obvious.
7. Update the README: the usage section and the **Coverage of the smsapi.bg
   API** table. The table must stay true in every commit.

## Compatibility

- The module is `v0` while the API settles. Still, prefer adding over
  changing: a new optional field or option rather than a changed signature.
  A breaking change gets a `!` in the commit type and a line in the release
  notes.
- Releases and tags are made by the owner only.

## Git

- Conventional Commits: `type(scope)!: lowercase description`, at most
  100 characters (`feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `ci`).
  Branches are `<type>/<description>`. Pull requests are squash-merged, so
  the title becomes the commit.
- Never commit a token, a `.env` file or a captured response that contains
  real phone numbers.
