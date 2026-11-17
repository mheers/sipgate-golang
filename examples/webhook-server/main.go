// Command webhook-server receives sipgate Push API webhooks, verifies their
// signature and prints the parsed event as JSON.
//
// The URL must be reachable from the internet and configured in the sipgate
// webhook settings. sipgate requires HTTPS and does not accept self-signed
// certificates.
//
// Usage:
//
//	go run ./examples/webhook-server :8080
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mheers/sipgate-golang/webhook"
)

func main() {
	address := ":8080"
	if len(os.Args) > 1 {
		address = os.Args[1]
	}

	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		event, body, err := webhook.ParseRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !webhook.VerifySignature(r.Header.Get("X-Sipgate-Signature"), body) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		// IsSipgateOrigin only works behind a reverse proxy that preserves
		// X-Forwarded-For. The signature check above is the primary defense.
		encoded, _ := json.MarshalIndent(event, "", "  ")
		log.Printf("webhook event:\n%s", encoded)

		// Respond with a Push API callflow (XML) if you want to control the
		// call. Returning 200 without a body continues normal call handling.
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              address,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on %s", address)
	log.Fatal(server.ListenAndServe())
}
