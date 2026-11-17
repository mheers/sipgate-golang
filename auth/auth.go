// Package auth implements sipgate OAuth 2.0 authentication for Go clients.
//
// sipgate uses Keycloak. The authorization code flow with PKCE is supported for
// public and confidential clients:
//
//	cfg := auth.Config{
//		ClientID:    os.Getenv("SIPGATE_CLIENT_ID"),
//		ClientSecret: os.Getenv("SIPGATE_CLIENT_SECRET"),
//		RedirectURL: "http://127.0.0.1:8080/callback",
//		Scopes:      []string{"account:read", "sms:write"},
//	}
//
//	verifier, _ := auth.GenerateVerifier()
//	authorizationURL, _ := cfg.AuthorizeURL(state, verifier)
//	// Open authorizationURL in a browser, receive the code on RedirectURL.
//	token, _ := cfg.Exchange(ctx, code, verifier)
//
//	tokenSource := cfg.TokenSource(ctx, token)
//	client, _ := sipgate.New(sipgate.WithTokenSource(tokenSource))
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

const (
	// Realm is the default sipgate Keycloak realm.
	Realm = "third-party"
	// DefaultIssuerURL is the issuer of the sipgate Keycloak realm.
	DefaultIssuerURL = "https://login.sipgate.com/auth/realms/" + Realm
	// DefaultAuthorizeURL is the OAuth 2.0 authorization endpoint.
	DefaultAuthorizeURL = DefaultIssuerURL + "/protocol/openid-connect/auth"
	// DefaultTokenURL is the OAuth 2.0 token endpoint.
	DefaultTokenURL = DefaultIssuerURL + "/protocol/openid-connect/token"
	// DefaultLogoutURL is the OpenID Connect RP-initiated logout endpoint.
	DefaultLogoutURL = DefaultIssuerURL + "/protocol/openid-connect/logout"
)

// Config describes an OAuth 2.0 client registered for the sipgate API.
type Config struct {
	// ClientID is the OAuth 2.0 client ID from the sipgate console.
	ClientID string
	// ClientSecret is optional for public clients using PKCE.
	ClientSecret string
	// RedirectURL must match a redirect URI registered for the client.
	RedirectURL string
	// Scopes requested during authorization, e.g. "sms:write".
	Scopes []string
	// AuthURL overrides the default authorization endpoint.
	AuthURL string
	// TokenURL overrides the default token endpoint.
	TokenURL string
	// LogoutURL overrides the default logout endpoint.
	LogoutURL string
	// HTTPClient is used for token requests. It is optional.
	HTTPClient *http.Client
}

// AuthorizeURL builds the URL the user has to open in a browser. codeVerifier
// is the PKCE verifier created with GenerateVerifier; it may be empty to omit
// PKCE (not recommended).
func (c Config) AuthorizeURL(state, codeVerifier string) (string, error) {
	if err := c.validate(true); err != nil {
		return "", err
	}
	var opts []oauth2.AuthCodeOption
	if codeVerifier != "" {
		opts = append(opts,
			oauth2.SetAuthURLParam("code_challenge", CodeChallenge(codeVerifier)),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
	}
	return c.oauth2Config().AuthCodeURL(state, opts...), nil
}

// Exchange trades an authorization code for an access and refresh token.
func (c Config) Exchange(ctx context.Context, code, codeVerifier string) (*oauth2.Token, error) {
	if err := c.validate(true); err != nil {
		return nil, err
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("auth: authorization code must not be empty")
	}
	var opts []oauth2.AuthCodeOption
	if codeVerifier != "" {
		opts = append(opts, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	}
	return c.oauth2Config().Exchange(c.httpContext(ctx), code, opts...)
}

// TokenSource returns a source that refreshes tokens automatically. The source
// is suitable for sipgate.WithTokenSource. The context is used for refreshes,
// so pass a long-lived context rather than the context of a single request.
func (c Config) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	if token == nil {
		return errorTokenSource{err: errors.New("auth: token must not be nil")}
	}
	return c.oauth2Config().TokenSource(c.httpContext(ctx), token)
}

// LogoutURLFor builds an OpenID Connect RP-initiated logout URL.
func (c Config) LogoutURLFor(idToken, postLogoutRedirectURI string) string {
	logoutURL := c.LogoutURL
	if logoutURL == "" {
		logoutURL = DefaultLogoutURL
	}
	u, err := url.Parse(logoutURL)
	if err != nil {
		return logoutURL
	}
	q := u.Query()
	if c.ClientID != "" {
		q.Set("client_id", c.ClientID)
	}
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	if postLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c Config) oauth2Config() *oauth2.Config {
	authURL := c.AuthURL
	if authURL == "" {
		authURL = DefaultAuthorizeURL
	}
	tokenURL := c.TokenURL
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	authStyle := oauth2.AuthStyleAutoDetect
	if c.ClientSecret == "" {
		// Public clients authenticate with client_id in the request body.
		authStyle = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Scopes:       c.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   authURL,
			TokenURL:  tokenURL,
			AuthStyle: authStyle,
		},
	}
}

func (c Config) httpContext(ctx context.Context) context.Context {
	if c.HTTPClient == nil {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, c.HTTPClient)
}

func (c Config) validate(requireRedirect bool) error {
	if strings.TrimSpace(c.ClientID) == "" {
		return errors.New("auth: client ID must not be empty")
	}
	authURL := c.AuthURL
	if authURL == "" {
		authURL = DefaultAuthorizeURL
	}
	if err := validateEndpoint("authorization", authURL); err != nil {
		return err
	}
	tokenURL := c.TokenURL
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	if err := validateEndpoint("token", tokenURL); err != nil {
		return err
	}
	if c.RedirectURL == "" {
		if requireRedirect {
			return errors.New("auth: redirect URL must not be empty")
		}
		return nil
	}
	u, err := url.Parse(c.RedirectURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("auth: invalid redirect URL %q", c.RedirectURL)
	}
	return nil
}

func validateEndpoint(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("auth: invalid %s URL", name)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("auth: invalid %s URL %q", name, u.Redacted())
	}
	return nil
}

// GenerateVerifier creates a PKCE code verifier as defined by RFC 7636.
func GenerateVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate PKCE verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CodeChallenge derives the S256 PKCE challenge for a verifier.
func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type errorTokenSource struct {
	err error
}

func (s errorTokenSource) Token() (*oauth2.Token, error) {
	return nil, s.err
}
