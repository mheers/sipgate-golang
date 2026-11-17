// Command send-sms sends an SMS through a sipgate SMS extension.
//
// Usage:
//
//	SIPGATE_TOKEN_ID=token-... SIPGATE_TOKEN=... \
//	  go run ./examples/send-sms s0 +4915799912345 "Hello from Go"
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mheers/sipgate-golang"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatalf("usage: %s <smsId> <recipient> <message>", os.Args[0])
	}

	client, err := sipgate.New(
		sipgate.WithPersonalAccessToken(os.Getenv("SIPGATE_TOKEN_ID"), os.Getenv("SIPGATE_TOKEN")),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = client.SendSMS(ctx, sipgate.SMS{
		SMSID:     os.Args[1],
		Recipient: os.Args[2],
		Message:   os.Args[3],
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("SMS sent")
}
