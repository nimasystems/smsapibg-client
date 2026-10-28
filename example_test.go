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
	case errors.Is(err, smsapibg.ErrDuplicateIdx):
		fmt.Println("already sent by an earlier attempt: treat as sent")
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

// Receive delivery reports. The handler is yours; it stores the reports,
// then answers "OK" so smsapi.bg stops resending them.
func ExampleParseDeliveryReport() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reports, err := smsapibg.ParseDeliveryReport(r.URL.Query())
		if err != nil {
			http.Error(w, "bad report", http.StatusBadRequest)
			return
		}
		for _, rep := range reports {
			// Store rep, keyed by rep.MessageID; a repeat is a no-op.
			fmt.Println(rep.MessageID, rep.Idx, rep.Status, rep.Status.Final(), rep.Status.Delivered())
		}
		fmt.Fprint(w, "OK")
	})

	// What smsapi.bg sends: one GET, two reports, comma-separated.
	req := httptest.NewRequest(http.MethodGet,
		"/sms/dlr?MsgId=17000000000000001,17000000000000002&status=404,410"+
			"&status_name=DELIVERED,ACCEPTED&idx=verify-42,verify-43&donedate=1761652900,1761652901&to=359888123456,359888654321", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	fmt.Println(rec.Body.String())
	// Output:
	// 17000000000000001 verify-42 DELIVERED true true
	// 17000000000000002 verify-43 ACCEPTED false false
	// OK
}
