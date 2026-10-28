package smsapibg

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAnAPIErrorMatchesTheClassesItsCodeBelongsTo(t *testing.T) {
	all := map[string]error{
		"ErrInsufficientCredits": ErrInsufficientCredits,
		"ErrUnauthorized":        ErrUnauthorized,
		"ErrInvalidNumber":       ErrInvalidNumber,
		"ErrRateLimited":         ErrRateLimited,
		"ErrBlocked":             ErrBlocked,
		"ErrTemporary":           ErrTemporary,
	}
	cases := []struct {
		code int
		want []string
	}{
		{CodeRequestError, []string{"ErrTemporary"}},
		{CodeMessageInvalid, nil},
		{CodeNoValidNumbers, []string{"ErrInvalidNumber"}},
		{CodeWrongSenderName, nil},
		{CodeTooManyAttemptsToNumber, []string{"ErrRateLimited"}},
		{CodeDuplicateIdx, nil},
		{CodeBlacklisted, []string{"ErrBlocked"}},
		{CodeOptedOut, []string{"ErrBlocked"}},
		{CodeBadAuthorization, []string{"ErrUnauthorized"}},
		{CodeBadCredentials, []string{"ErrUnauthorized"}},
		{CodeInsufficientCredits, []string{"ErrInsufficientCredits"}},
		{CodeSubmissionFailed, []string{"ErrTemporary"}},
		{CodeInternalError, []string{"ErrTemporary"}},
		{CodeTooManySimultaneous, []string{"ErrRateLimited", "ErrTemporary"}},
		{CodeTooManyRequests, []string{"ErrRateLimited", "ErrTemporary"}},
		{CodeTokenLacksPermission, []string{"ErrUnauthorized"}},
		{CodeHTTPSRequired, nil},
		{CodeSystemError, []string{"ErrTemporary"}},
	}
	for _, tc := range cases {
		err := error(&APIError{Code: tc.code, Message: "m", HTTPStatus: http.StatusOK})
		wrapped := fmt.Errorf("send: %w", err)
		for name, sentinel := range all {
			want := false
			for _, w := range tc.want {
				if w == name {
					want = true
				}
			}
			if got := errors.Is(wrapped, sentinel); got != want {
				t.Errorf("code %d: errors.Is(%s) = %v, want %v", tc.code, name, got, want)
			}
		}
	}
}

func TestAnAPIErrorWithInvalidNumbersIsAnInvalidNumberError(t *testing.T) {
	err := &APIError{Code: CodeRequestError, InvalidNumbers: []InvalidNumber{{Number: "1", Message: "Invalid phone number"}}}
	if !errors.Is(err, ErrInvalidNumber) {
		t.Error("an error that lists invalid numbers is not ErrInvalidNumber")
	}
}

func TestTemporaryIsTrueOnlyForRetryableErrors(t *testing.T) {
	cases := []struct {
		err  *APIError
		want bool
	}{
		{&APIError{Code: CodeRequestError}, true},
		{&APIError{Code: CodeSubmissionFailed}, true},
		{&APIError{Code: CodeInternalError}, true},
		{&APIError{Code: CodeTooManySimultaneous}, true},
		{&APIError{Code: CodeTooManyRequests}, true},
		{&APIError{Code: CodeSystemError}, true},
		{&APIError{Code: CodeInsufficientCredits}, false},
		{&APIError{Code: CodeNoValidNumbers}, false},
		{&APIError{Code: CodeTooManyAttemptsToNumber}, false},
		{&APIError{HTTPStatus: http.StatusServiceUnavailable}, true},
		{&APIError{HTTPStatus: http.StatusTooManyRequests}, true},
		{&APIError{HTTPStatus: http.StatusBadRequest}, false},
		{&APIError{Code: CodeInsufficientCredits, HTTPStatus: http.StatusServiceUnavailable}, false},
	}
	for _, tc := range cases {
		if got := tc.err.Temporary(); got != tc.want {
			t.Errorf("%+v Temporary() = %v, want %v", *tc.err, got, tc.want)
		}
	}
}

func TestAnHTTPStatusClassifiesAnErrorThatHasNoCode(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusBadGateway, ErrTemporary},
	}
	for _, tc := range cases {
		if err := (&APIError{HTTPStatus: tc.status}); !errors.Is(err, tc.want) {
			t.Errorf("HTTP %d is not %v", tc.status, tc.want)
		}
	}
	if errors.Is(&APIError{HTTPStatus: http.StatusBadRequest}, ErrTemporary) {
		t.Error("HTTP 400 is ErrTemporary")
	}
}

func TestAStringErrorIDIsKeptAsItsName(t *testing.T) {
	err := &APIError{Name: "authorization_failed", Message: "Authorization failed", HTTPStatus: http.StatusOK}
	if !errors.Is(err, ErrUnauthorized) {
		t.Error("authorization_failed is not ErrUnauthorized")
	}
	if !strings.Contains(err.Error(), "authorization_failed") {
		t.Errorf("Error() = %q, want the error name", err.Error())
	}
}

func TestAnAPIErrorMessageNamesTheCodeButNotTheNumbers(t *testing.T) {
	err := &APIError{
		Code:           CodeNoValidNumbers,
		Message:        "No correct phone numbers",
		HTTPStatus:     http.StatusOK,
		InvalidNumbers: []InvalidNumber{{Number: "359888123456", SubmittedNumber: "0888123456", Message: "Invalid phone number"}},
	}
	got := err.Error()
	for _, want := range []string{"13", "No correct phone numbers"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, want it to contain %q", got, want)
		}
	}
	for _, leak := range []string{"359888123456", "0888123456"} {
		if strings.Contains(got, leak) {
			t.Errorf("Error() = %q leaks a phone number", got)
		}
	}
}

func TestAnAPIErrorWithoutABodyNamesTheHTTPStatus(t *testing.T) {
	err := &APIError{HTTPStatus: http.StatusBadGateway}
	if got := err.Error(); !strings.Contains(got, "502") {
		t.Errorf("Error() = %q, want the HTTP status", got)
	}
}

func TestAValidationErrorNamesTheFieldButNotTheValue(t *testing.T) {
	err := error(&ValidationError{Field: "to", Reason: "must contain digits only"})
	if got := err.Error(); !strings.Contains(got, "to") || !strings.Contains(got, "digits only") {
		t.Errorf("Error() = %q, want the field and the reason", got)
	}
	var ve *ValidationError
	if !errors.As(fmt.Errorf("wrap: %w", err), &ve) || ve.Field != "to" {
		t.Error("errors.As does not find the *ValidationError")
	}
}

func TestALocalValidationErrorOnTheNumberIsAnInvalidNumberError(t *testing.T) {
	if !errors.Is(&ValidationError{Field: "to", Reason: "x"}, ErrInvalidNumber) {
		t.Error("a bad 'to' is not ErrInvalidNumber")
	}
	if errors.Is(&ValidationError{Field: "message", Reason: "x"}, ErrInvalidNumber) {
		t.Error("a bad 'message' is ErrInvalidNumber")
	}
}
