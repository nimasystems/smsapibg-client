package smsapibg_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"

	smsapibg "github.com/nimasystems/smsapibg-client"
)

// standIn plays smsapi.bg for the examples, so they run without a token or
// a network. It answers a test=1 send the way the real API does.
func standIn() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fmt.Fprintf(w, `{"count":1,"list":[{"id":"17000000000000001","points":0.16,"number":%q,"date_sent":1761652800,"status":"QUEUE"}]}`,
			r.PostForm.Get("to"))
	}))
}

// Send a verification code in test mode: smsapi.bg checks the message and
// answers as if it sent it, but sends nothing and charges nothing.
func ExampleClient_Send() {
	api := standIn() // stand-in for https://api.smsapi.bg/; leave it out in real code
	defer api.Close()

	token := os.Getenv("SMSAPIBG_TOKEN") // from the customer portal; never hard-code it
	if token == "" {
		token = "example-token" // the stand-in accepts anything
	}
	client, err := smsapibg.New(token,
		smsapibg.WithUserAgent("example-app/1.0"),
		smsapibg.WithBaseURL(api.URL), // leave this out to use https://api.smsapi.bg/
	)
	if err != nil {
		log.Fatal(err)
	}

	res, err := client.Send(context.Background(), smsapibg.Message{
		To:        "+359888123456",
		Text:      "Your code is 123456",
		Test:      true,
		Idx:       "verify-42", // your reference; with CheckIdx a retry cannot send twice
		CheckIdx:  true,
		NoUnicode: true,
	})
	switch {
	case errors.Is(err, smsapibg.ErrInvalidNumber):
		fmt.Println("ask the user for another number")
	case errors.Is(err, smsapibg.ErrTemporary):
		fmt.Println("retry later")
	case err != nil:
		log.Fatal(err)
	default:
		fmt.Println(res.ID, res.Number, res.Status)
	}
	// Output: 17000000000000001 359888123456 QUEUE
}
