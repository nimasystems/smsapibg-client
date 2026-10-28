package smsapibg

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseDeliveryReportReadsASingleReport(t *testing.T) {
	q, _ := url.ParseQuery("MsgId=1460969715572091219&status=404&status_name=DELIVERED&idx=verify-42" +
		"&donedate=1460969800&sent_at=1460969712&username=owner&points=0.16&to=359888123456&mcc=284&mnc=1&from=NotSkip")

	got, err := ParseDeliveryReport(q)
	if err != nil {
		t.Fatalf("ParseDeliveryReport: %v", err)
	}
	want := []DeliveryReport{{
		MessageID:  "1460969715572091219",
		Status:     StatusDelivered,
		StatusName: "DELIVERED",
		To:         "359888123456",
		Idx:        "verify-42",
		DoneAt:     time.Unix(1460969800, 0),
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %+v\nwant      %+v", got, want)
	}
}

func TestParseDeliveryReportSplitsCommaSeparatedReports(t *testing.T) {
	q := url.Values{
		"MsgId":       {"1,2,3"},
		"status":      {"404,405,403"},
		"status_name": {"DELIVERED,UNDELIVERED,SENT"},
		"to":          {"359888000001,359888000002,359888000003"},
		"idx":         {"a,,c"},
		"donedate":    {"1700000001,1700000002,"},
	}
	got, err := ParseDeliveryReport(q)
	if err != nil {
		t.Fatalf("ParseDeliveryReport: %v", err)
	}
	want := []DeliveryReport{
		{MessageID: "1", Status: StatusDelivered, StatusName: "DELIVERED", To: "359888000001", Idx: "a", DoneAt: time.Unix(1700000001, 0)},
		{MessageID: "2", Status: StatusUndelivered, StatusName: "UNDELIVERED", To: "359888000002", Idx: "", DoneAt: time.Unix(1700000002, 0)},
		{MessageID: "3", Status: StatusSent, StatusName: "SENT", To: "359888000003", Idx: "c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %+v\nwant      %+v", got, want)
	}
}

func TestParseDeliveryReportNeedsOnlyTheMessageIDAndStatus(t *testing.T) {
	got, err := ParseDeliveryReport(url.Values{"MsgId": {"9"}, "status": {"409"}})
	if err != nil {
		t.Fatalf("ParseDeliveryReport: %v", err)
	}
	want := []DeliveryReport{{MessageID: "9", Status: StatusQueue}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %+v, want %+v", got, want)
	}
}

func TestParseDeliveryReportKeepsAStatusCodeItDoesNotKnow(t *testing.T) {
	got, err := ParseDeliveryReport(url.Values{"MsgId": {"9"}, "status": {"499"}, "status_name": {"NEW_ONE"}})
	if err != nil {
		t.Fatalf("ParseDeliveryReport: %v", err)
	}
	if got[0].Status != DeliveryStatus(499) || got[0].StatusName != "NEW_ONE" {
		t.Errorf("report = %+v", got[0])
	}
	if got[0].Status.Final() || got[0].Status.Delivered() {
		t.Error("an unknown status claims to be final or delivered")
	}
}

func TestParseDeliveryReportRefusesAMalformedCallback(t *testing.T) {
	cases := []struct {
		name  string
		query url.Values
	}{
		{"no message id", url.Values{"status": {"404"}}},
		{"an empty message id", url.Values{"MsgId": {""}, "status": {"404"}}},
		{"an empty message id in a list", url.Values{"MsgId": {"1,,3"}, "status": {"404,404,404"}}},
		{"no status", url.Values{"MsgId": {"1"}}},
		{"a status that is not a number", url.Values{"MsgId": {"1"}, "status": {"DELIVERED"}}},
		{"fewer statuses than ids", url.Values{"MsgId": {"1,2"}, "status": {"404"}}},
		{"more numbers than ids", url.Values{"MsgId": {"1"}, "status": {"404"}, "to": {"359888000001,359888000002"}}},
		{"a done date that is not unix time", url.Values{"MsgId": {"1"}, "status": {"404"}, "donedate": {"2026-10-28"}}},
		{"mismatched idx count", url.Values{"MsgId": {"1,2"}, "status": {"404,404"}, "idx": {"a"}}},
		{"mismatched status name count", url.Values{"MsgId": {"1,2"}, "status": {"404,404"}, "status_name": {"DELIVERED"}}},
	}
	for _, tc := range cases {
		_, err := ParseDeliveryReport(tc.query)
		if !errors.Is(err, ErrMalformedReport) {
			t.Errorf("%s: error = %v, want ErrMalformedReport", tc.name, err)
		}
	}
}

func TestAMalformedReportErrorNeverEchoesTheValues(t *testing.T) {
	_, err := ParseDeliveryReport(url.Values{"MsgId": {"1"}, "status": {"404"}, "to": {"359888000001,359888000002"}})
	if err == nil || strings.Contains(err.Error(), "359888") {
		t.Errorf("error = %v, want one without the phone numbers", err)
	}
}

func TestDeliveryStatusesKnowWhetherTheyAreFinalAndDelivered(t *testing.T) {
	cases := []struct {
		status           DeliveryStatus
		name             string
		final, delivered bool
	}{
		{StatusNotFound, "NOT_FOUND", true, false},
		{StatusExpired, "EXPIRED", true, false},
		{StatusSent, "SENT", false, false},
		{StatusDelivered, "DELIVERED", true, true},
		{StatusUndelivered, "UNDELIVERED", true, false},
		{StatusFailed, "FAILED", true, false},
		{StatusRejected, "REJECTED", true, false},
		{StatusUnknown, "UNKNOWN", true, false},
		{StatusQueue, "QUEUE", false, false},
		{StatusAccepted, "ACCEPTED", false, false},
		{StatusStop, "STOP", true, false},
	}
	for _, tc := range cases {
		if got := tc.status.String(); got != tc.name {
			t.Errorf("%d.String() = %q, want %q", int(tc.status), got, tc.name)
		}
		if got := tc.status.Final(); got != tc.final {
			t.Errorf("%s.Final() = %v, want %v", tc.name, got, tc.final)
		}
		if got := tc.status.Delivered(); got != tc.delivered {
			t.Errorf("%s.Delivered() = %v, want %v", tc.name, got, tc.delivered)
		}
	}
}

func TestAnUnknownDeliveryStatusPrintsItsCode(t *testing.T) {
	if got := DeliveryStatus(499).String(); got != "DeliveryStatus(499)" {
		t.Errorf("String() = %q, want DeliveryStatus(499)", got)
	}
}
