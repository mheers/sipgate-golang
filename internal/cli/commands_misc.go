package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/mheers/sipgate-golang/openapi"
	"github.com/spf13/cobra"
)

func (a *App) eventsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Inspect historically triggered app events",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List app events",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			events, err := result(client.AppAPI.GetEvents(cmd.Context()).Execute())
			if err != nil {
				return err
			}
			return a.print(events)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "delete <eventId>",
		Short: "Delete an app event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 32)
			if err != nil {
				return errors.New("eventId must be a number")
			}
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := check(client.AppAPI.DeleteEvent(cmd.Context(), int32(id)).Execute()); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "deleted", "eventId": id})
		},
	})
	return cmd
}

func (a *App) webhooksCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhooks",
		Short: "Manage sipgate.io webhook settings",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get",
		Short: "Show the account webhook settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			settings, err := result(client.SettingsAPI.GetSipgateIoUrls(cmd.Context()).Execute())
			if err != nil {
				return err
			}
			return a.print(settings)
		},
	})

	var (
		incoming, outgoing string
		logEnabled         bool
		pushAPIVersion     int
		whitelist          []string
	)
	set := &cobra.Command{
		Use:   "set",
		Short: "Update the account webhook settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			if incoming == "" || outgoing == "" {
				return errors.New("--incoming-url and --outgoing-url are required")
			}
			if pushAPIVersion < 0 {
				return errors.New("--push-api-version must not be negative")
			}
			body := openapi.SetSipgateIoAccountSettingsRequest{
				IncomingUrl: incoming,
				OutgoingUrl: outgoing,
				Whitelist:   whitelist,
			}
			if cmd.Flags().Changed("log") {
				body.Log = &logEnabled
			}
			if cmd.Flags().Changed("push-api-version") {
				version := int32(pushAPIVersion)
				body.PushApiVersion = &version
			}
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			settings, err := result(client.SettingsAPI.SetSipgateIoUrls(cmd.Context()).Body(body).Execute())
			if err != nil {
				return err
			}
			return a.print(settings)
		},
	}
	set.Flags().StringVar(&incoming, "incoming-url", "", "URL for incoming call webhooks")
	set.Flags().StringVar(&outgoing, "outgoing-url", "", "URL for outgoing call webhooks")
	set.Flags().BoolVar(&logEnabled, "log", false, "enable webhook logging")
	set.Flags().IntVar(&pushAPIVersion, "push-api-version", 0, "Push API version (1 or 2)")
	set.Flags().StringSliceVar(&whitelist, "whitelist", nil, "extension, group or channel IDs to activate webhooks for")

	cmd.AddCommand(set, &cobra.Command{
		Use:   "log",
		Short: "Show recently sent webhooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			entries, err := result(client.LogAPI.GetWebhooksLog(cmd.Context()).Execute())
			if err != nil {
				return err
			}
			return a.print(entries)
		},
	})
	return cmd
}

func (a *App) apiCommand() *cobra.Command {
	var (
		data    string
		headers []string
		query   []string
	)
	cmd := &cobra.Command{
		Use:   "api <method> <path>",
		Short: "Send an arbitrary API request",
		Long: "Send a request relative to the configured API base URL. This is an escape hatch for endpoints that\n" +
			"the CLI does not wrap. The response body is printed as JSON when possible.",
		Example: "  sipgate api GET /account\n" +
			"  sipgate api POST /sessions/sms --data '{\"smsId\":\"s0\",\"recipient\":\"+49...\",\"message\":\"hi\"}'\n" +
			"  sipgate api GET /history --query limit=5 --query type=SMS",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
				return errors.New("path must be relative to the API base URL")
			}

			if len(query) > 0 {
				params := url.Values{}
				for _, entry := range query {
					key, value, ok := strings.Cut(entry, "=")
					if !ok {
						return fmt.Errorf("invalid --query %q (use key=value)", entry)
					}
					params.Add(key, value)
				}
				separator := "?"
				if strings.Contains(path, "?") {
					separator = "&"
				}
				path += separator + params.Encode()
			}

			var body io.Reader
			if data != "" {
				body = strings.NewReader(data)
			}
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			request, err := client.NewRequest(cmd.Context(), method, path, body)
			if err != nil {
				return err
			}
			contentTypeSet := false
			for _, header := range headers {
				key, value, ok := strings.Cut(header, ":")
				if !ok {
					return fmt.Errorf("invalid --header %q (use Name: value)", header)
				}
				key = strings.TrimSpace(key)
				if strings.EqualFold(key, "Content-Type") {
					contentTypeSet = true
				}
				request.Header.Set(key, strings.TrimSpace(value))
			}
			if data != "" && !contentTypeSet {
				contentType := "text/plain"
				if json.Valid([]byte(data)) {
					contentType = "application/json"
				}
				request.Header.Set("Content-Type", contentType)
			}

			response, err := client.Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			responseBody, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
			if err != nil {
				return err
			}
			if len(bytes.TrimSpace(responseBody)) == 0 {
				return nil
			}

			if json.Valid(responseBody) {
				return a.print(json.RawMessage(responseBody))
			}
			_, err = a.Out.Write(responseBody)
			if err != nil {
				return err
			}
			if !bytes.HasSuffix(responseBody, []byte("\n")) {
				a.printString("")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "request body")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "request header, repeatable (Name: value)")
	cmd.Flags().StringArrayVar(&query, "query", nil, "query parameter, repeatable (key=value)")
	return cmd
}
