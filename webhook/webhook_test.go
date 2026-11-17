package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Test vector from the official sipgateio-node webhook test suite. The body is
// byte-for-byte the payload that sipgate signed.
const (
	signedBody = "callId=&direction=in&event=newCall&from=4912354678&fullUserId%5B%5D%5B0%5D=123456789&originalCallId=&to=49999999&user%5B%5D%5B0%5D=TestUser&userId%5B%5D%5B0%5D=123456789&xcid="
	signature  = "hlY7r9Vad0NP/7xJxf+vcDqjWaGWHOcIrj+rcP5aqQQcHtbSLsElp2kRNRPBL5unWbq6bExVPZB49HHM+Y/fWSVL19q7KSJhYPHfikcME0r0mCYB4S/VnJwnIvpiqz6s7Dpnk3wDCy65B3WQLwBVWA9oh6ojNM/g+87YnoMTKRx1KoFqosKNfBp1c1I8XjXusGOW/VlGnMb6wHhUVdwi9K7FfUgxj2pnV+M1Xv9rYs6RAi4V1OcUPqdT5geHsxWa09sk+AEHSUm1EFnAvx7PhIkugpNwST7yPKHf0+iyei4qUQCBZtfQVOI4mLZTRfQuyVo3YuJfvHaNPYY34/1ZCZGCKeu+HS6WHs1vGUyKxSi8v4JJqog2VOlWruf8pMGg+syuAFwuxiCnWsSXgaaUfe9JrBAFjBxUmNP9DzR1bbkwxkJnthacu7jALXjGsubjSSSl955QgenV/ZpODHgWDPg0fe6qGILtk+kXLjyfSsoR/qgzE5W5OAyZq8W64h01KAt9Q283N7/2nogy6keiIWL3qjPolWnrchSP7iJUatM2YiTcpkNKnJ70UE05cdw3swuNe7zqD51MdOX3rAioEOFgOIFMSrxMVX+V4XK7sa5o43smN8lHoa+0AogQMuIrC7k2axdRbulSSNfyqVAZIT4qS0cItiv3aPXsdDKkkA0="
)

func TestParseFormNewCall(t *testing.T) {
	event, err := ParseForm([]byte(signedBody))
	if err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}
	if event.Type != EventNewCall {
		t.Errorf("Type = %q", event.Type)
	}
	if event.Direction != DirectionIn {
		t.Errorf("Direction = %q", event.Direction)
	}
	if event.From != "4912354678" || event.To != "49999999" {
		t.Errorf("From/To = %q/%q", event.From, event.To)
	}
	if len(event.Users) != 1 || event.Users[0] != "TestUser" {
		t.Errorf("Users = %v", event.Users)
	}
	if len(event.UserIDs) != 1 || event.UserIDs[0] != "123456789" {
		t.Errorf("UserIDs = %v", event.UserIDs)
	}
	if len(event.FullUserIDs) != 1 || event.FullUserIDs[0] != "123456789" {
		t.Errorf("FullUserIDs = %v", event.FullUserIDs)
	}
	if event.CallID != "" || event.OriginalCallID != "" {
		t.Errorf("CallID/OriginalCallID = %q/%q", event.CallID, event.OriginalCallID)
	}
	if event.Raw.Get("event") != "newCall" {
		t.Errorf("Raw = %v", event.Raw)
	}
}

func TestParseRepeatedUsers(t *testing.T) {
	body := "event=newCall&callId=123&from=%2B492111234567&to=%2B4915791234567&direction=in&user%5B%5D=Alice&user%5B%5D=Bob&userId%5B%5D=w0&userId%5B%5D=w1&fullUserId%5B%5D=1234567w0&fullUserId%5B%5D=1234567w1&xcid=abc"
	event, err := ParseForm([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Users) != 2 || event.Users[0] != "Alice" || event.Users[1] != "Bob" {
		t.Errorf("Users = %v", event.Users)
	}
	if len(event.UserIDs) != 2 || event.UserIDs[1] != "w1" {
		t.Errorf("UserIDs = %v", event.UserIDs)
	}
	if event.From != "+492111234567" || event.To != "+4915791234567" {
		t.Errorf("From/To = %q/%q", event.From, event.To)
	}
	if event.XCID != "abc" {
		t.Errorf("XCID = %q", event.XCID)
	}
}

func TestParseAnswer(t *testing.T) {
	body := "event=answer&callId=123&user=John+Doe&userId=w0&fullUserId=1234567w0&from=anonymous&to=%2B4915791234567&direction=out&answeringNumber=%2B4921199999999&diversion=%2B4921100000000"
	event, err := ParseForm([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventAnswer {
		t.Errorf("Type = %q", event.Type)
	}
	if len(event.Users) != 1 || event.Users[0] != "John Doe" {
		t.Errorf("Users = %v", event.Users)
	}
	if event.AnsweringNumber != "+4921199999999" {
		t.Errorf("AnsweringNumber = %q", event.AnsweringNumber)
	}
	if event.Diversion != "+4921100000000" {
		t.Errorf("Diversion = %q", event.Diversion)
	}
	if event.From != "anonymous" {
		t.Errorf("From = %q", event.From)
	}
}

func TestParseHangup(t *testing.T) {
	body := "event=hangup&cause=normalClearing&callId=123&from=49111&to=49222&direction=in&answeringNumber=49333&origCallId=999"
	event, err := ParseForm([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventHangup {
		t.Errorf("Type = %q", event.Type)
	}
	if event.Cause != CauseNormalClearing {
		t.Errorf("Cause = %q", event.Cause)
	}
	if event.OriginalCallID != "999" {
		t.Errorf("OriginalCallID = %q", event.OriginalCallID)
	}
}

func TestParseDTMF(t *testing.T) {
	event, err := ParseForm([]byte("event=dtmf&callId=123&dtmf=0123"))
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventDTMF || event.DTMF != "0123" {
		t.Errorf("event = %+v", event)
	}
}

func TestParseUnknownEventIsPreserved(t *testing.T) {
	event, err := ParseForm([]byte("event=recording&callId=123&channelId=abc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(event.Type) != "recording" {
		t.Errorf("Type = %q", event.Type)
	}
	if event.ChannelID != "abc" {
		t.Errorf("ChannelID = %q", event.ChannelID)
	}
}

func TestParseRequiresEvent(t *testing.T) {
	if _, err := ParseForm([]byte("callId=123")); err == nil {
		t.Fatal("ParseForm() accepted a payload without event")
	}
}

func TestParseValues(t *testing.T) {
	event, err := ParseValues(map[string][]string{
		"event":           {"newCall"},
		"user[]":          {"Alice", "Bob"},
		"user[][0]":       {"ignored-by-key-order"},
		"fullUserId[0]":   {"123w0"},
		"origCallId":      {"orig"},
		"direction":       {"out"},
		"answeringNumber": {"+49300"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.OriginalCallID != "orig" || event.Direction != DirectionOut {
		t.Errorf("event = %+v", event)
	}
	if len(event.Users) != 3 {
		t.Errorf("Users = %v", event.Users)
	}
}

func TestParseRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(signedBody))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	event, body, err := ParseRequest(r)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if !bytes.Equal(body, []byte(signedBody)) {
		t.Error("returned body differs from request body")
	}
	if event.Type != EventNewCall {
		t.Errorf("Type = %q", event.Type)
	}
}

func TestParseRequestRejectsWrongContentType(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader("event=newCall"))
	r.Header.Set("Content-Type", "application/json")
	if _, _, err := ParseRequest(r); err == nil {
		t.Fatal("ParseRequest() accepted application/json")
	}
}

func TestParseRequestRejectsOversizedBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(bytes.Repeat([]byte("a"), MaxBodySize+1)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, _, err := ParseRequest(r); err == nil {
		t.Fatal("ParseRequest() accepted an oversized body")
	}
}

func TestVerifySignature(t *testing.T) {
	if !VerifySignature(signature, []byte(signedBody)) {
		t.Fatal("VerifySignature() rejected the official test vector")
	}
	if VerifySignature(signature, []byte(signedBody+"&x=1")) {
		t.Error("VerifySignature() accepted a tampered body")
	}
	if VerifySignature("not-base64", []byte(signedBody)) {
		t.Error("VerifySignature() accepted an invalid signature encoding")
	}
	if VerifySignature("", []byte(signedBody)) {
		t.Error("VerifySignature() accepted an empty signature")
	}
}

func TestIsSipgateOrigin(t *testing.T) {
	tests := []struct {
		name string
		xff  string
		want bool
	}{
		{name: "first official IP", xff: "217.116.118.254", want: true},
		{name: "second official IP", xff: "212.9.46.32", want: true},
		{name: "list", xff: "10.0.0.1, 217.116.118.254", want: true},
		{name: "foreign IP", xff: "127.0.0.1", want: false},
		{name: "missing", xff: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/hook", nil)
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := IsSipgateOrigin(r); got != tt.want {
				t.Errorf("IsSipgateOrigin() = %v, want %v", got, tt.want)
			}
		})
	}
}
