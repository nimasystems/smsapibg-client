package smsapibg

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProfileGetsTheBalanceWithTheBearerToken(t *testing.T) {
	var method, path, auth, accept string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, auth, accept = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"name":"Example Ltd","email":"owner@example.com","username":"owner",` +
			`"phone_number":"359888000000","payment_type":"prepaid","user_type":"native","points":123.45}`))
	})

	p, err := c.Profile(t.Context())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if method != http.MethodGet || path != "/profile" {
		t.Errorf("request = %s %s, want GET /profile", method, path)
	}
	if auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the bearer token", auth)
	}
	if accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", accept)
	}
	want := Profile{
		Points:      123.45,
		Name:        "Example Ltd",
		Email:       "owner@example.com",
		Username:    "owner",
		PhoneNumber: "359888000000",
		PaymentType: "prepaid",
		UserType:    "native",
	}
	if p != want {
		t.Errorf("Profile = %+v, want %+v", p, want)
	}
}

func TestProfileAuthorizationFailureIsUnauthorized(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Authorization failed","error":"authorization_failed","developer_description":null,"errors":null}`))
	})

	_, err := c.Profile(t.Context())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Profile error = %v, want ErrUnauthorized", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Profile error is %T, want *APIError", err)
	}
	if apiErr.Name != "authorization_failed" || apiErr.HTTPStatus != http.StatusUnauthorized || apiErr.Message != "Authorization failed" {
		t.Errorf("APIError = %+v", *apiErr)
	}
}

func TestProfileAcceptsPointsSentAsAString(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"points":"10.5"}`))
	})
	p, err := c.Profile(t.Context())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if p.Points != 10.5 {
		t.Errorf("Points = %v, want 10.5", p.Points)
	}
}

func TestProfileRefusesAMalformedBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"points":`))
	})
	if _, err := c.Profile(t.Context()); !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("Profile error = %v, want ErrUnexpectedResponse", err)
	}
}

func TestProfile200WithAnErrorBodyIsAnAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":101,"message":"Invalid authorization info"}`))
	})
	_, err := c.Profile(t.Context())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != CodeBadAuthorization || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Profile error = %v, want an *APIError 101 that is ErrUnauthorized", err)
	}
}

func TestProfileNon2xxWithoutAJSONBodyIsAnAPIErrorWithTheStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("<html>down</html>"))
	})
	_, err := c.Profile(t.Context())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusServiceUnavailable || apiErr.Code != 0 {
		t.Fatalf("Profile error = %v, want an *APIError with HTTP 503 and no code", err)
	}
	if strings.Contains(err.Error(), "html") {
		t.Errorf("the error echoes the body: %v", err)
	}
}

func TestProfileStopsWhenTheContextIsCancelled(t *testing.T) {
	arrived := make(chan struct{})
	c := newHangingClient(t, arrived)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { <-arrived; cancel() }()
	if _, err := c.Profile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Profile error = %v, want context.Canceled", err)
	}
}

func TestProfileTimesOutOnASlowServer(t *testing.T) {
	c := newHangingClient(t, nil, WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}))
	_, err := c.Profile(t.Context())
	var timeout interface{ Timeout() bool }
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("Profile error = %v, want a timeout", err)
	}
}
