# sipgate-golang

A Go SDK and command line client for the [sipgate REST API v2](https://api.sipgate.com/v2/doc).

The client is generated from sipgate's official Swagger document with OpenAPI Generator and wrapped in a small, hand-written SDK that adds personal access token and OAuth 2.0 authentication, retries, typed errors, webhook support and a Cobra CLI.

- 188 operations and 218 models from the official spec, fully typed
- Personal access tokens (HTTP Basic) and OAuth 2.0 authorization code + PKCE with automatic refresh
- Automatic retries with exponential backoff for idempotent requests
- Typed `*sipgate.APIError` with status and RFC 7807 details
- Push API webhook parsing and RSA-SHA256 signature verification
- `sipgate` CLI with JSON/YAML output, OAuth login and a raw request escape hatch
- No hand-maintained endpoint list: the generated client is reproducible with `make generate`

Requires Go 1.23 or newer.

## Installation

```bash
go get github.com/mheers/sipgate-golang
```

Install the CLI:

```bash
go install github.com/mheers/sipgate-golang/cmd/sipgate@latest
```

## Authentication

sipgate supports two authentication methods.

### Personal access token (recommended for scripts)

Create a token in the sipgate account under *Personal Access Token*. sipgate returns a token ID and a secret; both are sent as HTTP Basic credentials.

```go
client, err := sipgate.New(
    sipgate.WithPersonalAccessToken(os.Getenv("SIPGATE_TOKEN_ID"), os.Getenv("SIPGATE_TOKEN")),
)
if err != nil {
    log.Fatal(err)
}
```

For the CLI:

```bash
export SIPGATE_TOKEN_ID=token-FQ1V12
export SIPGATE_TOKEN=e68ead46-a7db-46cd-8a1a-44aed1e4e372
sipgate account get
```

### OAuth 2.0

Create an OAuth client in the sipgate console, register a loopback redirect URI and request the scopes your application needs. The SDK implements the authorization code flow with PKCE; `auth.Config` handles token exchange and refresh.

```go
cfg := auth.Config{
    ClientID:     os.Getenv("SIPGATE_CLIENT_ID"),
    ClientSecret: os.Getenv("SIPGATE_CLIENT_SECRET"), // optional for public clients
    RedirectURL:  "http://127.0.0.1:8080/callback",
    Scopes:       []string{"account:read", "sms:write"},
}

// The token source uses this context for refreshes, so pass a long-lived
// context rather than a single request's context.
ctx := context.Background()

verifier, err := auth.GenerateVerifier()
if err != nil {
    log.Fatal(err)
}
state := "random-state-stored-in-the-user-session"
authorizationURL, err := cfg.AuthorizeURL(state, verifier)
if err != nil {
    log.Fatal(err)
}
// Redirect the user to authorizationURL. Validate state when the code arrives
// at RedirectURL, then exchange it:
token, err := cfg.Exchange(ctx, code, verifier)
if err != nil {
    log.Fatal(err)
}

client, err := sipgate.New(sipgate.WithTokenSource(cfg.TokenSource(ctx, token)))
```

The token source is consulted for every request, so short-lived access tokens (sipgate issues them for about five minutes) are refreshed transparently.

#### OAuth login with the CLI

```bash
export SIPGATE_CLIENT_ID=2556404-0-dc848ae6-085c-11e8-92a6-31b99c83912e:third-party
export SIPGATE_CLIENT_SECRET=...            # omit for public clients
sipgate auth login --scope account:read --scope sms:write
```

The command prints an authorization URL, opens a browser and listens on `http://127.0.0.1:8080/callback`. That redirect URI must be registered for the OAuth client. Use `--redirect-uri` to change it and `--no-browser` on headless machines.

The tokens are stored in the CLI config with mode `0600`:

- Linux: `~/.config/sipgate/config.yaml`
- macOS: `~/Library/Application Support/sipgate/config.yaml`

`SIPGATE_CONFIG` and `--config` override the path. `auth login` also stores the OAuth client ID and secret when provided; prefer environment variables if you do not want the secret on disk.

```bash
sipgate auth status
sipgate auth logout
```

## Library usage

### Quick start

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/mheers/sipgate-golang"
)

func main() {
    client, err := sipgate.New(
        sipgate.WithPersonalAccessToken(os.Getenv("SIPGATE_TOKEN_ID"), os.Getenv("SIPGATE_TOKEN")),
    )
    if err != nil {
        log.Fatal(err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    account, err := client.Account(ctx)
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("%s %s", account.GetFirstName(), account.GetLastName())

    err = client.SendSMS(ctx, sipgate.SMS{
        SMSID:     "s0",
        Recipient: "+4915799912345",
        Message:   "Hello from Go",
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

Convenience methods included in the SDK:

| Method | Endpoint |
| --- | --- |
| `Account(ctx)` | `GET /account` |
| `Balance(ctx)` | `GET /balance` |
| `Userinfo(ctx)` | `GET /authorization/userinfo` |
| `SendSMS(ctx, SMS)` | `POST /sessions/sms` |
| `CreateCall(ctx, CallRequest)` | `POST /sessions/calls` |
| `Calls(ctx)` | `GET /calls` |
| `HangupCall(ctx, callID)` | `DELETE /calls/{callId}` |

Everything else is available through the generated API services, which are embedded in `sipgate.Client`:

```go
extensions, resp, err := client.SmsAPI.GetSmsExtensions(ctx, "w0").Execute()
contacts, resp, err := client.ContactsAPI.GetContacts1(ctx).Limit(20).Execute()
history, resp, err := client.HistoryAPI.GetHistory(ctx).Limit(10).Types([]string{"SMS"}).Execute()
fax, resp, err := client.SessionsAPI.SendFax(ctx).Body(request).Execute()
```

The generated client is documented in the `openapi` package and mirrors the [sipgate API reference](https://api.sipgate.com/v2/doc) one-to-one. API services include `AccountAPI`, `AddressesAPI`, `AppAPI`, `AuthorizationAPI`, `AutorecordingsAPI`, `BlacklistAPI`, `CallrestrictionsAPI`, `ChannelsAPI`, `ContactsAPI`, `DevicesAPI`, `FaxlinesAPI`, `ForwardingsAPI`, `GroupsAPI`, `HistoryAPI`, `LogAPI`, `NotificationsAPI`, `NumbersAPI`, `PhonelinesAPI`, `PortingsAPI`, `RestrictionsAPI`, `RtcmAPI`, `SessionsAPI`, `SettingsAPI`, `SmsAPI`, `UrlsAPI`, `UsersAPI` and `VoicemailsAPI`.

### Error handling

Every non-2xx response is converted into `*sipgate.APIError` by the convenience methods and by `sipgate.WrapError`. The generated client's raw errors can be converted as well.

```go
account, err := client.Account(ctx)
if err != nil {
    if apiErr, ok := sipgate.AsAPIError(err); ok {
        log.Printf("sipgate returned %d: %s (%s)", apiErr.StatusCode, apiErr.Title, apiErr.Detail)
    }
    switch {
    case sipgate.IsUnauthorized(err):
        // token expired or lacks a scope
    case sipgate.IsNotFound(err):
        // resource does not exist
    case sipgate.IsRateLimited(err):
        // reduce concurrency
    }
    return err
}
```

`APIError` exposes `StatusCode`, `Title`, `Detail` and the raw `Body`.

### Retries

The SDK retries network errors and selected responses with exponential backoff:

- `GET`, `HEAD`, `OPTIONS`, `PUT` and `DELETE` are retried on network errors, `500`, `502`, `503` and `504`.
- `POST` is only retried on `429 Too Many Requests`, so an SMS or fax is never sent twice because of a server error.
- `Retry-After` is honoured, capped at the configured maximum backoff.
- The default is two retries; configure with `sipgate.WithRetries`, `sipgate.WithRetryBackoff` and `sipgate.WithTimeout`.

### Raw requests

For endpoints that are not needed from the typed client, or for debugging, use `NewRequest` and `Do`:

```go
req, err := client.NewRequest(ctx, http.MethodGet, "/calls", nil)
if err != nil {
    return err
}
resp, err := client.Do(req)
if err != nil {
    return err
}
defer resp.Body.Close()
```

`Do` applies authentication and retries and returns a typed `*APIError` for non-2xx responses.

### Webhooks

sipgate sends webhook requests as `application/x-www-form-urlencoded` and signs the body with RSA-SHA256 in the `X-Sipgate-Signature` header. The `webhook` package parses the payload and verifies the signature:

```go
func handle(w http.ResponseWriter, r *http.Request) {
    event, body, err := webhook.ParseRequest(r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    if !webhook.VerifySignature(r.Header.Get("X-Sipgate-Signature"), body) {
        http.Error(w, "invalid signature", http.StatusUnauthorized)
        return
    }

    switch event.Type {
    case webhook.EventNewCall:
        log.Printf("call %s from %s to %s (%s)", event.CallID, event.From, event.To, event.Direction)
    case webhook.EventAnswer:
        log.Printf("call %s answered by %v", event.CallID, event.Users)
    case webhook.EventHangup:
        log.Printf("call %s ended: %s", event.CallID, event.Cause)
    case webhook.EventDTMF:
        log.Printf("call %s received digits %s", event.CallID, event.DTMF)
    }
}
```

`event.Raw` contains all form values, including fields added by newer Push API versions. `webhook.IsSipgateOrigin` checks `X-Forwarded-For` against sipgate's Push API addresses when the server runs behind a reverse proxy; only trust it if that proxy overwrites `X-Forwarded-For`, because the header is otherwise client-controlled. See `examples/webhook-server` for a runnable server.

## CLI

```text
sipgate <command> [flags]
```

All commands print JSON by default; use `-o yaml` for YAML. Errors go to stderr and the exit code is non-zero on failure.

### Commands

| Command | Description |
| --- | --- |
| `sipgate auth login` | OAuth 2.0 login with PKCE and local callback server |
| `sipgate auth status` | Show the local authentication state |
| `sipgate auth logout` | Delete stored access and refresh tokens (keeps the OAuth client registration) |
| `sipgate account get` | Account data |
| `sipgate account balance` | Account balance |
| `sipgate whoami` | Authenticated user |
| `sipgate sms list [--user w0]` | SMS extensions (defaults to the authenticated user) |
| `sipgate sms send --sms-id s0 --to +49... --message "Hi"` | Send an SMS |
| `sipgate calls list` | Established calls |
| `sipgate calls start --caller e0 --callee +49...` | Start a call |
| `sipgate calls hangup <callId>` | Hang up a call |
| `sipgate calls dtmf <callId> --sequence 123#` | Send DTMF digits |
| `sipgate history list --limit 5 --type SMS` | History entries |
| `sipgate history get <entryId>` | Single history entry |
| `sipgate contacts list` | Contacts |
| `sipgate contacts get <contactId>` | Single contact |
| `sipgate contacts create --name "Ada Lovelace" --email ada@example.com` | Create a contact |
| `sipgate contacts update <contactId> --note "..."` | Update a contact |
| `sipgate contacts delete <contactId>` | Delete a contact |
| `sipgate addresses list`, `addresses get <id>` | Addresses |
| `sipgate devices list [--user w0]`, `devices get <id>` | Devices (defaults to the authenticated user) |
| `sipgate numbers list [--user w0]` | Phone numbers |
| `sipgate phonelines list [--user w0]`, `phonelines get <id> [--user w0]` | Phone lines |
| `sipgate users list`, `users get <userId>` | Users |
| `sipgate voicemails list`, `voicemails get <id>` | Voicemails |
| `sipgate fax send --faxline f0 --to +49... --file invoice.pdf` | Send a fax |
| `sipgate events list`, `events delete <id>` | App events |
| `sipgate webhooks get`, `webhooks set`, `webhooks log` | Push API settings |
| `sipgate api <method> <path>` | Arbitrary authenticated API request |
| `sipgate completion <shell>` | Shell completion (Cobra) |

User-scoped commands (`sms list`, `devices list`, `phonelines get`) default to the authenticated user, which is resolved through `GET /authorization/userinfo`. Pass `--user` to manage another member of the account.

### Examples

```bash
# Send an SMS with a personal access token
SIPGATE_TOKEN_ID=token-... SIPGATE_TOKEN=... \
  sipgate sms send --sms-id s0 --to +4915799912345 --message "Deployed"

# Start a call and later hang it up
session=$(sipgate calls start --caller e0 --callee +4915799912345 | jq -r .sessionId)
sipgate calls hangup "$session"

# Show the last five SMS history entries as YAML
sipgate history list --limit 5 --type SMS -o yaml

# Call an endpoint the CLI does not wrap
sipgate api GET /numbers
sipgate api POST /sessions/sms \
  --data '{"smsId":"s0","recipient":"+4915799912345","message":"Hello"}'

# Query parameters and headers
sipgate api GET /history --query limit=5 --query types=SMS --header "X-Debug: 1"
```

### Configuration and environment

Precedence: explicit flag, then environment variable, then config file, then default. Explicit `--access-token` and `--token-id`/`--token` cannot be combined; a flag of either type also overrides environment credentials of the other type.

| Flag | Environment | Purpose |
| --- | --- | --- |
| `--token-id` | `SIPGATE_TOKEN_ID` | Personal access token ID |
| `--token` | `SIPGATE_TOKEN` | Personal access token secret |
| `--access-token` | `SIPGATE_ACCESS_TOKEN` | OAuth 2.0 access token |
| `--base-url` | `SIPGATE_BASE_URL` | API base URL |
| `--config` | `SIPGATE_CONFIG` | Config file path |
| `--output`, `-o` | `SIPGATE_OUTPUT` | `json` or `yaml` |
| `--retries` | | Retries for idempotent requests |

The OAuth login command reads `SIPGATE_CLIENT_ID`, `SIPGATE_CLIENT_SECRET`, `SIPGATE_REDIRECT_URI`, `SIPGATE_AUTH_URL`, `SIPGATE_TOKEN_URL` and `SIPGATE_SCOPES` (comma separated). Prefer environment variables or the config file over `--token`, `--access-token` and `--client-secret`; command-line arguments can be visible to other local users in process listings.

Config file example:

```yaml
base_url: https://api.sipgate.com/v2
auth:
  type: pat
  token_id: token-FQ1V12
  token: e68ead46-a7db-46cd-8a1a-44aed1e4e372
```

The file is written with mode `0600`. Do not commit it.

## Project layout

```text
.
├── openapi/                 generated OpenAPI client (do not edit)
├── sipgate.go               SDK client, options, retries, raw requests
├── api.go                   convenience methods
├── errors.go                APIError and helpers
├── transport.go             authentication and retry transports
├── auth/                    OAuth 2.0 + PKCE helpers
├── webhook/                 Push API parsing and signature verification
├── internal/cli/            Cobra commands, config and output
├── cmd/sipgate/             CLI entry point
├── examples/                runnable examples
├── spec/sipgate-v2.json     normalized upstream Swagger document
├── tools/specgen            spec downloader and normalizer
└── tools/genfix             post-generation identifier fix
```

## Development

The hand-written packages were developed test-first. Run the suite with:

```bash
make test          # go test ./...
make race          # go test -race ./...
make cover         # coverage summary
make vet           # go vet ./...
make lint          # vet + formatting check
make build         # bin/sipgate with version ldflags
make e2e           # read-only smoke test against the live API
```

`make e2e` reads `SIPGATE_TOKEN_ID` and `SIPGATE_TOKEN` (or `SIPGATE_ACCESS_TOKEN`) from the untracked `.env.dev` file (mode `0600`) and only performs read-only requests. It never sends messages, starts calls or changes account settings.

### Regenerating the client

The upstream document is Swagger 2.0. `tools/specgen` downloads it, resolves two duplicate operation IDs, removes parameter fields that Swagger 2.0 does not allow and corrects `addressId` in `/numbers` responses (sipgate returns a string although the spec declares an integer). The normalized document is checked in, so `make generate` is reproducible and does not need the live sipgate API:

```bash
make generate               # spec/sipgate-v2.json -> openapi/
make generate-from-upstream # refresh spec from api.sipgate.com, then generate
```

Generation uses OpenAPI Generator `7.27.0`, pinned in `openapitools.json`, and requires Java 11+ and Node.js for `npx`. The generated code is not edited by hand. `tools/genfix` applies one documented post-generation fix for a Go identifier collision in `ApiUrlsResponse`; CI regenerates the client and fails if the result differs from the checked-in code.

### API coverage

The generated client covers all 188 operations of the upstream spec. The CLI wraps the everyday operations listed above and exposes every other endpoint through `sipgate api`. New upstream endpoints are picked up by `make generate-from-upstream`; the wrapper packages do not need to change.

## License

MIT. Copyright (c) 2026 Marcel Heers <marcel@heers.it>.
