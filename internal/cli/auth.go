package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/mheers/sipgate-golang"
	"github.com/mheers/sipgate-golang/auth"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

type loginSettings struct {
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	redirectURI  string
	scopes       []string
	noBrowser    bool
	timeout      time.Duration
}

func (a *App) authCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate against sipgate",
	}
	cmd.AddCommand(a.authLoginCommand(), a.authStatusCommand(), a.authLogoutCommand())
	return cmd
}

func (a *App) authLoginCommand() *cobra.Command {
	settings := loginSettings{
		clientID:     os.Getenv(envClientID),
		clientSecret: os.Getenv(envClientSecret),
		authURL:      os.Getenv(envAuthURL),
		tokenURL:     os.Getenv(envTokenURL),
		redirectURI:  os.Getenv(envRedirectURI),
		scopes:       splitList([]string{os.Getenv(envScopes)}),
		timeout:      5 * time.Minute,
	}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with OAuth 2.0 (authorization code + PKCE)",
		Long: "Authenticate with the sipgate OAuth 2.0 authorization code flow.\n\n" +
			"Create an OAuth client in the sipgate console and pass its client ID via\n" +
			"--client-id or " + envClientID + ". The redirect URI must be registered for\n" +
			"the client and point to a loopback address.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.authLogin(cmd.Context(), settings)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&settings.clientID, "client-id", settings.clientID, "OAuth client ID (env "+envClientID+")")
	flags.StringVar(&settings.clientSecret, "client-secret", settings.clientSecret, "OAuth client secret (env "+envClientSecret+")")
	flags.StringVar(&settings.authURL, "auth-url", settings.authURL, "authorization endpoint")
	flags.StringVar(&settings.tokenURL, "token-url", settings.tokenURL, "token endpoint")
	flags.StringVar(&settings.redirectURI, "redirect-uri", settings.redirectURI, "loopback redirect URI (env "+envRedirectURI+")")
	flags.StringArrayVar(&settings.scopes, "scope", settings.scopes, "OAuth scopes, repeatable (env "+envScopes+")")
	flags.BoolVar(&settings.noBrowser, "no-browser", false, "only print the authorization URL")
	flags.DurationVar(&settings.timeout, "timeout", settings.timeout, "time to wait for the browser callback")
	// Do not print an environment-provided client secret in --help.
	if flag := flags.Lookup("client-secret"); flag != nil {
		flag.DefValue = ""
	}
	return cmd
}

func (a *App) authLogin(ctx context.Context, settings loginSettings) error {
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}

	if settings.clientID == "" {
		settings.clientID = cfg.OAuth.ClientID
	}
	if settings.clientSecret == "" {
		settings.clientSecret = cfg.OAuth.ClientSecret
	}
	if settings.authURL == "" {
		settings.authURL = cfg.OAuth.AuthURL
	}
	if settings.tokenURL == "" {
		settings.tokenURL = cfg.OAuth.TokenURL
	}
	if settings.redirectURI == "" {
		settings.redirectURI = firstNonEmpty(cfg.OAuth.RedirectURL, defaultRedirectURI)
	}
	if len(settings.scopes) == 0 {
		settings.scopes = cfg.OAuth.Scopes
	}
	if settings.timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	redirect, err := loopbackRedirect(settings.redirectURI)
	if err != nil {
		return err
	}
	oauthCfg := auth.Config{
		ClientID:     settings.clientID,
		ClientSecret: settings.clientSecret,
		RedirectURL:  settings.redirectURI,
		Scopes:       settings.scopes,
		AuthURL:      settings.authURL,
		TokenURL:     settings.tokenURL,
	}

	verifier, err := auth.GenerateVerifier()
	if err != nil {
		return err
	}
	state, err := randomState()
	if err != nil {
		return err
	}
	authorizeURL, err := oauthCfg.AuthorizeURL(state, verifier)
	if err != nil {
		return fmt.Errorf("build authorization URL: %w", err)
	}

	listener, err := net.Listen("tcp", redirect.Host)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", redirect.Host, err)
	}

	fmt.Fprintf(a.Err, "Open this URL in your browser:\n\n%s\n\n", authorizeURL)
	if !settings.noBrowser && a.openBrowser != nil {
		if err := a.openBrowser(authorizeURL); err != nil {
			fmt.Fprintf(a.Err, "Could not open a browser: %v\n", err)
		}
	}

	code, err := a.waitForCode(ctx, listener, redirect.Path, state, settings.timeout)
	if err != nil {
		return err
	}

	token, err := oauthCfg.Exchange(ctx, code, verifier)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	if token.AccessToken == "" {
		return errors.New("token endpoint returned an empty access token")
	}

	cfg.OAuth = OAuthConfig{
		ClientID:     settings.clientID,
		ClientSecret: settings.clientSecret,
		AuthURL:      settings.authURL,
		TokenURL:     settings.tokenURL,
		RedirectURL:  settings.redirectURI,
		Scopes:       settings.scopes,
	}
	if a.BaseURL != "" {
		cfg.BaseURL = a.BaseURL
	}
	cfg.Auth = AuthConfig{
		Type:         AuthTypeOAuth,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		IDToken:      idToken(token),
	}
	if !token.Expiry.IsZero() {
		expiry := token.Expiry
		cfg.Auth.Expiry = &expiry
	}
	if err := SaveConfig(path, cfg); err != nil {
		return fmt.Errorf("save authentication: %w", err)
	}

	return a.print(map[string]any{
		"status":     "authenticated",
		"type":       AuthTypeOAuth,
		"config":     path,
		"expires_at": cfg.Auth.Expiry,
		"scopes":     settings.scopes,
	})
}

func (a *App) waitForCode(ctx context.Context, listener net.Listener, path, state string, timeout time.Duration) (string, error) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if message := query.Get("error"); message != "" {
			detail := query.Get("error_description")
			errCh <- fmt.Errorf("authorization failed: %s %s", message, detail)
			http.Error(w, "Authorization failed. You can close this window.", http.StatusBadRequest)
			return
		}
		if query.Get("state") != state {
			errCh <- errors.New("authorization callback has an invalid state")
			http.Error(w, "Invalid state. You can close this window.", http.StatusBadRequest)
			return
		}
		code := query.Get("code")
		if code == "" {
			errCh <- errors.New("authorization callback has no code")
			http.Error(w, "Missing code. You can close this window.", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("<html><body><h1>Authentication successful</h1><p>You can close this window and return to the terminal.</p></body></html>"))
		codeCh <- code
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case code := <-codeCh:
		return code, nil
	case err := <-errCh:
		return "", err
	case <-timer.C:
		return "", fmt.Errorf("timed out after %s waiting for the browser callback", timeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a *App) authStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current authentication state",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := a.loadConfig()
			if err != nil {
				return err
			}
			baseURL := firstNonEmpty(a.BaseURL, cfg.BaseURL, sipgate.DefaultBaseURL)

			status := map[string]any{
				"authenticated": false,
				"config":        path,
				"base_url":      baseURL,
			}
			flags := cmd.Flags()
			if flags.Changed("access-token") && (flags.Changed("token-id") || flags.Changed("token")) {
				status["error"] = "use either --access-token or --token-id/--token, not both"
				return a.print(status)
			}
			switch {
			case flags.Changed("access-token"):
				status["authenticated"] = a.AccessToken != ""
				status["type"] = AuthTypeOAuth
			case flags.Changed("token-id") || flags.Changed("token"):
				status["authenticated"] = a.TokenID != "" && a.Token != ""
				status["type"] = AuthTypePAT
			case a.TokenID != "" || a.Token != "":
				status["authenticated"] = a.TokenID != "" && a.Token != ""
				status["type"] = AuthTypePAT
			case a.AccessToken != "":
				status["authenticated"] = true
				status["type"] = AuthTypeOAuth
			case cfg.Auth.Type == AuthTypePAT || (cfg.Auth.Type == "" && cfg.Auth.TokenID != ""):
				status["authenticated"] = cfg.Auth.TokenID != "" && cfg.Auth.Token != ""
				status["type"] = AuthTypePAT
			case cfg.Auth.Type == AuthTypeOAuth || (cfg.Auth.Type == "" && cfg.Auth.AccessToken != ""):
				status["authenticated"] = cfg.Auth.AccessToken != ""
				status["type"] = AuthTypeOAuth
				if cfg.Auth.Expiry != nil {
					status["expires_at"] = cfg.Auth.Expiry
					if cfg.Auth.Expiry.Before(a.nowOrTime()) {
						status["expired"] = true
						if cfg.Auth.RefreshToken == "" {
							status["authenticated"] = false
						}
					}
				}
				if len(cfg.OAuth.Scopes) > 0 {
					status["scopes"] = cfg.OAuth.Scopes
				}
			}
			return a.print(status)
		},
	}
}

func (a *App) authLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored access and refresh tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := a.loadConfig()
			if err != nil {
				return err
			}
			cfg.Auth = AuthConfig{}
			if err := SaveConfig(path, cfg); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "logged_out", "config": path})
		},
	}
}

func loopbackRedirect(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid redirect URI %q: %w", raw, err)
	}
	if u.User != nil {
		return nil, errors.New("redirect URI must not contain userinfo")
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("redirect URI host must be a loopback address, got %q", host)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("redirect URI must use http, got %q", u.Scheme)
	}
	if u.Port() == "" {
		return nil, errors.New("redirect URI must contain an explicit port")
	}
	if u.Port() == "0" {
		return nil, errors.New("redirect URI must use a non-zero port")
	}
	if u.Path == "" || u.Path == "/" {
		return nil, errors.New("redirect URI must contain a path, for example /callback")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("redirect URI must not contain a query or fragment")
	}
	return u, nil
}

func randomState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func idToken(token *oauth2.Token) string {
	if value, ok := token.Extra("id_token").(string); ok {
		return value
	}
	return ""
}

func openBrowser(rawURL string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{rawURL}
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", rawURL}
	default:
		command = "xdg-open"
		args = []string{rawURL}
	}
	cmd := exec.Command(command, args...)
	return cmd.Start()
}
