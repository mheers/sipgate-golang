package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mheers/sipgate-golang"
	"github.com/mheers/sipgate-golang/auth"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

const (
	envConfig       = "SIPGATE_CONFIG"
	envBaseURL      = "SIPGATE_BASE_URL"
	envTokenID      = "SIPGATE_TOKEN_ID"
	envToken        = "SIPGATE_TOKEN"
	envAccessToken  = "SIPGATE_ACCESS_TOKEN"
	envOutput       = "SIPGATE_OUTPUT"
	envClientID     = "SIPGATE_CLIENT_ID"
	envClientSecret = "SIPGATE_CLIENT_SECRET"
	envRedirectURI  = "SIPGATE_REDIRECT_URI"
	envAuthURL      = "SIPGATE_AUTH_URL"
	envTokenURL     = "SIPGATE_TOKEN_URL"
	envScopes       = "SIPGATE_SCOPES"
)

const defaultRedirectURI = "http://127.0.0.1:8080/callback"

// App is the sipgate CLI. It is a separate instance per invocation, which keeps
// command state and tests isolated.
type App struct {
	Out io.Writer
	Err io.Writer

	ConfigPath  string
	BaseURL     string
	TokenID     string
	Token       string
	AccessToken string
	Output      string
	Retries     int

	openBrowser func(string) error
	now         func() time.Time
}

// NewApp creates a CLI instance with defaults from the environment.
func NewApp() *App {
	app := &App{
		Out:         os.Stdout,
		Err:         os.Stderr,
		ConfigPath:  os.Getenv(envConfig),
		BaseURL:     os.Getenv(envBaseURL),
		TokenID:     os.Getenv(envTokenID),
		Token:       os.Getenv(envToken),
		AccessToken: os.Getenv(envAccessToken),
		Output:      os.Getenv(envOutput),
		Retries:     2,
		openBrowser: openBrowser,
		now:         time.Now,
	}
	if app.Output == "" {
		app.Output = "json"
	}
	return app
}

// Execute runs the CLI with the process arguments and returns the exit code.
func (a *App) Execute(ctx context.Context) int {
	if err := a.Run(ctx, os.Args[1:]...); err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	return 0
}

// Run executes the CLI with the given arguments. It is used by tests.
func (a *App) Run(ctx context.Context, args ...string) error {
	cmd := a.command()
	cmd.SetArgs(args)
	cmd.SetOut(a.Out)
	cmd.SetErr(a.Err)
	return cmd.ExecuteContext(ctx)
}

func (a *App) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "sipgate",
		Short:         "Command line interface for the sipgate REST API",
		Long:          "sipgate is a command line client for the sipgate REST API v2. It supports personal access tokens and OAuth 2.0.",
		Version:       sipgate.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if a.Output != "json" && a.Output != "yaml" {
				return fmt.Errorf("unsupported output format %q (use json or yaml)", a.Output)
			}
			if a.Retries < 0 {
				return errors.New("retries must not be negative")
			}
			return nil
		},
	}
	cmd.SetVersionTemplate("sipgate {{.Version}}\n")

	flags := cmd.PersistentFlags()
	flags.StringVar(&a.ConfigPath, "config", a.ConfigPath, "configuration file (env "+envConfig+")")
	flags.StringVar(&a.BaseURL, "base-url", a.BaseURL, "API base URL (env "+envBaseURL+")")
	flags.StringVar(&a.TokenID, "token-id", a.TokenID, "personal access token ID (env "+envTokenID+")")
	flags.StringVar(&a.Token, "token", a.Token, "personal access token (env "+envToken+")")
	flags.StringVar(&a.AccessToken, "access-token", a.AccessToken, "OAuth 2.0 access token (env "+envAccessToken+")")
	flags.StringVarP(&a.Output, "output", "o", a.Output, "output format: json or yaml")
	flags.IntVar(&a.Retries, "retries", a.Retries, "retries for idempotent requests")

	cmd.AddCommand(
		a.authCommand(),
		a.accountCommand(),
		a.whoamiCommand(),
		a.smsCommand(),
		a.callsCommand(),
		a.historyCommand(),
		a.contactsCommand(),
		a.addressesCommand(),
		a.devicesCommand(),
		a.numbersCommand(),
		a.phonelinesCommand(),
		a.usersCommand(),
		a.voicemailsCommand(),
		a.faxCommand(),
		a.eventsCommand(),
		a.webhooksCommand(),
		a.apiCommand(),
	)
	return cmd
}

func (a *App) loadConfig() (*Config, string, error) {
	path := a.ConfigPath
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, "", err
		}
	}
	path = expandHome(path)
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

// newClient builds an authenticated SDK client from flags, environment and the
// config file, in that order of precedence.
func (a *App) newClient(ctx context.Context) (*sipgate.Client, error) {
	cfg, path, err := a.loadConfig()
	if err != nil {
		return nil, err
	}

	baseURL := firstNonEmpty(a.BaseURL, cfg.BaseURL, sipgate.DefaultBaseURL)
	opts := []sipgate.Option{
		sipgate.WithBaseURL(baseURL),
		sipgate.WithUserAgent("sipgate-cli/" + sipgate.Version),
		sipgate.WithRetries(a.Retries),
	}

	switch {
	case a.TokenID != "" || a.Token != "":
		if a.TokenID == "" || a.Token == "" {
			return nil, errors.New("both --token-id and --token (or " + envTokenID + " and " + envToken + ") are required for personal access token authentication")
		}
		opts = append(opts, sipgate.WithPersonalAccessToken(a.TokenID, a.Token))
	case a.AccessToken != "":
		opts = append(opts, sipgate.WithToken(a.AccessToken))
	default:
		credential, err := a.credentialOptionFromConfig(ctx, cfg, path)
		if err != nil {
			return nil, err
		}
		opts = append(opts, credential)
	}

	return sipgate.New(opts...)
}

// credentialOptionFromConfig returns the authentication option stored in the
// config file, refreshing an expired OAuth 2.0 access token if necessary.
func (a *App) credentialOptionFromConfig(ctx context.Context, cfg *Config, path string) (sipgate.Option, error) {
	switch cfg.Auth.Type {
	case AuthTypePAT:
		if cfg.Auth.TokenID == "" || cfg.Auth.Token == "" {
			return nil, errors.New("incomplete personal access token in config; run sipgate auth login or set " + envTokenID + " and " + envToken)
		}
		return sipgate.WithPersonalAccessToken(cfg.Auth.TokenID, cfg.Auth.Token), nil
	case AuthTypeOAuth, "":
		if cfg.Auth.AccessToken == "" {
			return nil, errors.New("not authenticated; run sipgate auth login or provide --access-token")
		}
		token := &oauth2.Token{
			AccessToken:  cfg.Auth.AccessToken,
			RefreshToken: cfg.Auth.RefreshToken,
		}
		if cfg.Auth.Expiry != nil {
			token.Expiry = *cfg.Auth.Expiry
		}
		if cfg.Auth.Expiry == nil || (token.Valid() && a.now().Add(30*time.Second).Before(token.Expiry)) {
			return sipgate.WithToken(token.AccessToken), nil
		}
		if token.RefreshToken == "" {
			return sipgate.WithToken(token.AccessToken), nil
		}

		oauthCfg := auth.Config{
			ClientID:     cfg.OAuth.ClientID,
			ClientSecret: cfg.OAuth.ClientSecret,
			AuthURL:      cfg.OAuth.AuthURL,
			TokenURL:     cfg.OAuth.TokenURL,
			RedirectURL:  cfg.OAuth.RedirectURL,
		}
		if oauthCfg.ClientID == "" {
			return nil, errors.New("cannot refresh the access token: OAuth client ID is missing; run sipgate auth login again")
		}
		refreshed, err := oauthCfg.TokenSource(ctx, token).Token()
		if err != nil {
			return nil, fmt.Errorf("refresh access token: %w", err)
		}

		cfg.Auth.AccessToken = refreshed.AccessToken
		if refreshed.RefreshToken != "" {
			cfg.Auth.RefreshToken = refreshed.RefreshToken
		}
		if refreshed.Expiry.IsZero() {
			cfg.Auth.Expiry = nil
		} else {
			expiry := refreshed.Expiry
			cfg.Auth.Expiry = &expiry
		}
		if err := SaveConfig(path, cfg); err != nil {
			fmt.Fprintf(a.Err, "warning: could not persist refreshed token: %v\n", err)
		}
		return sipgate.WithToken(refreshed.AccessToken), nil
	default:
		return nil, fmt.Errorf("unknown auth type %q in config", cfg.Auth.Type)
	}
}

func (a *App) oauthConfig(cfg *Config) auth.Config {
	return auth.Config{
		ClientID:     cfg.OAuth.ClientID,
		ClientSecret: cfg.OAuth.ClientSecret,
		AuthURL:      cfg.OAuth.AuthURL,
		TokenURL:     cfg.OAuth.TokenURL,
		RedirectURL:  cfg.OAuth.RedirectURL,
		Scopes:       cfg.OAuth.Scopes,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// result converts the (value, response, error) triple of a generated API call
// into a value or a typed API error.
func result[T any](value T, resp *http.Response, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, sipgate.WrapError(resp, err)
	}
	return value, nil
}

func check(resp *http.Response, err error) error {
	if err != nil {
		return sipgate.WrapError(resp, err)
	}
	return nil
}

func splitList(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}
