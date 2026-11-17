package cli

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mheers/sipgate-golang"
	"github.com/mheers/sipgate-golang/openapi"
	"github.com/spf13/cobra"
)

func (a *App) accountCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Account information",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "get",
			Short: "Show account data",
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := a.newClient(cmd)
				if err != nil {
					return err
				}
				account, err := client.Account(cmd.Context())
				if err != nil {
					return err
				}
				return a.print(account)
			},
		},
		&cobra.Command{
			Use:   "balance",
			Short: "Show the account balance",
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := a.newClient(cmd)
				if err != nil {
					return err
				}
				balance, err := client.Balance(cmd.Context())
				if err != nil {
					return err
				}
				return a.print(balance)
			},
		},
	)
	return cmd
}

func (a *App) whoamiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the authenticated sipgate user",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			info, err := client.Userinfo(cmd.Context())
			if err != nil {
				return err
			}
			return a.print(info)
		},
	}
}

func (a *App) smsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sms",
		Short: "Send and inspect SMS extensions",
	}

	var listUser string
	list := &cobra.Command{
		Use:   "list",
		Short: "List SMS extensions",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			resolvedUserID, err := a.resolveUserID(cmd.Context(), client, listUser)
			if err != nil {
				return err
			}
			extensions, err := result(client.SmsAPI.GetSmsExtensions(cmd.Context(), resolvedUserID).Execute())
			if err != nil {
				return err
			}
			return a.print(extensions)
		},
	}
	list.Flags().StringVar(&listUser, "user", "", "user ID (default: the authenticated user)")

	var (
		smsID     string
		recipient string
		message   string
		sendAt    string
	)
	send := &cobra.Command{
		Use:   "send",
		Short: "Send an SMS",
		RunE: func(cmd *cobra.Command, args []string) error {
			if smsID == "" || recipient == "" || message == "" {
				return errors.New("--sms-id, --to and --message are required")
			}
			var at time.Time
			if sendAt != "" {
				parsed, err := time.Parse(time.RFC3339, sendAt)
				if err != nil {
					return fmt.Errorf("invalid --send-at (use RFC 3339): %w", err)
				}
				at = parsed
			}
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			if err := client.SendSMS(cmd.Context(), sipgate.SMS{
				SMSID:     smsID,
				Recipient: recipient,
				Message:   message,
				SendAt:    at,
			}); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "sent"})
		},
	}
	send.Flags().StringVar(&smsID, "sms-id", "", "SMS extension ID, for example s0")
	send.Flags().StringVar(&recipient, "to", "", "recipient number in E.164 format")
	send.Flags().StringVar(&message, "message", "", "message text")
	send.Flags().StringVar(&sendAt, "send-at", "", "schedule the message (RFC 3339)")

	cmd.AddCommand(list, send)
	return cmd
}

func (a *App) callsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "calls",
		Short: "Inspect and control calls",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List established calls",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			calls, err := client.Calls(cmd.Context())
			if err != nil {
				return err
			}
			return a.print(calls)
		},
	})

	var start sipgate.CallRequest
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start an outgoing call",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			sessionID, err := client.CreateCall(cmd.Context(), start)
			if err != nil {
				return err
			}
			return a.print(map[string]any{"sessionId": sessionID})
		},
	}
	startCmd.Flags().StringVar(&start.Caller, "caller", "", "calling extension ID, for example e0")
	startCmd.Flags().StringVar(&start.Callee, "callee", "", "destination number in E.164 format")
	startCmd.Flags().StringVar(&start.CallerID, "caller-id", "", "number presented to the callee")
	startCmd.Flags().StringVar(&start.DeviceID, "device-id", "", "route the call to this device")
	_ = startCmd.MarkFlagRequired("caller")
	_ = startCmd.MarkFlagRequired("callee")
	cmd.AddCommand(startCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "hangup <callId>",
		Short: "Hang up a call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			if err := client.HangupCall(cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "hangup", "callId": args[0]})
		},
	})

	var sequence string
	dtmf := &cobra.Command{
		Use:   "dtmf <callId>",
		Short: "Send DTMF digits to all participants",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sequence == "" {
				return errors.New("--sequence is required")
			}
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			if err := check(client.RtcmAPI.SendCallDtmf(cmd.Context(), args[0]).
				Body(openapi.DtmfRequest{Sequence: sequence}).Execute()); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "sent", "callId": args[0]})
		},
	}
	dtmf.Flags().StringVar(&sequence, "sequence", "", "DTMF sequence, for example 1234#")
	cmd.AddCommand(dtmf)

	return cmd
}

func (a *App) historyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Browse call, fax, SMS and voicemail history",
	}

	var (
		limit, offset              int
		types, directions, starred []string
		archived, includeAllUsers  bool
		from, to, phoneNumber      string
		connectionIDs              []string
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List history entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 || offset < 0 {
				return errors.New("--limit and --offset must not be negative")
			}
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			request := client.HistoryAPI.GetHistory(cmd.Context()).
				Limit(int32(limit)).
				Offset(int32(offset)).
				Archived(archived).
				IncludeAllUsers(includeAllUsers)
			if len(types) > 0 {
				request = request.Types(types)
			}
			if len(directions) > 0 {
				request = request.Directions(directions)
			}
			if len(starred) > 0 {
				request = request.Starred(starred)
			}
			if len(connectionIDs) > 0 {
				request = request.ConnectionIds(connectionIDs)
			}
			if from != "" {
				request = request.From(from)
			}
			if to != "" {
				request = request.To(to)
			}
			if phoneNumber != "" {
				request = request.Phonenumber(phoneNumber)
			}
			history, err := result(request.Execute())
			if err != nil {
				return err
			}
			return a.print(history)
		},
	}
	flags := list.Flags()
	flags.IntVar(&limit, "limit", 10, "maximum number of entries (0-1000)")
	flags.IntVar(&offset, "offset", 0, "pagination offset")
	flags.StringSliceVar(&types, "type", nil, "filter by type: CALL, VOICEMAIL, SMS, FAX")
	flags.StringSliceVar(&directions, "direction", nil, "filter by direction: INCOMING, OUTGOING, MISSED_INCOMING, MISSED_OUTGOING")
	flags.StringSliceVar(&starred, "starred", nil, "filter by STARRED or UNSTARRED")
	flags.BoolVar(&archived, "archived", false, "only show archived entries")
	flags.BoolVar(&includeAllUsers, "all-users", false, "include all users of the account (admins only)")
	flags.StringVar(&from, "from", "", "filter from date/time (ISO 8601)")
	flags.StringVar(&to, "to", "", "filter to date/time (ISO 8601)")
	flags.StringVar(&phoneNumber, "number", "", "filter by phone number")
	flags.StringSliceVar(&connectionIDs, "connection-id", nil, "filter by connection ID or channel UUID")

	cmd.AddCommand(list, &cobra.Command{
		Use:   "get <entryId>",
		Short: "Show a single history entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			entry, err := result(client.HistoryAPI.GetHistoryById(cmd.Context(), args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(entry)
		},
	})
	return cmd
}

func (a *App) faxCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "fax", Short: "Send faxes"}
	var (
		faxlineID string
		recipient string
		filename  string
	)
	send := &cobra.Command{
		Use:   "send",
		Short: "Send a fax",
		RunE: func(cmd *cobra.Command, args []string) error {
			if faxlineID == "" || recipient == "" || filename == "" {
				return errors.New("--faxline, --to and --file are required")
			}
			content, err := os.ReadFile(filename)
			if err != nil {
				return fmt.Errorf("read fax file: %w", err)
			}
			client, err := a.newClient(cmd)
			if err != nil {
				return err
			}
			body := openapi.SendFaxRequest{
				FaxlineId:     faxlineID,
				Recipient:     recipient,
				Filename:      filepath.Base(filename),
				Base64Content: base64.StdEncoding.EncodeToString(content),
			}
			session, err := result(client.SessionsAPI.SendFax(cmd.Context()).Body(body).Execute())
			if err != nil {
				return err
			}
			return a.print(map[string]any{"sessionId": session.GetSessionId()})
		},
	}
	send.Flags().StringVar(&faxlineID, "faxline", "", "faxline ID, for example f0")
	send.Flags().StringVar(&recipient, "to", "", "recipient number in E.164 format")
	send.Flags().StringVar(&filename, "file", "", "path to a PDF or TIFF file")
	_ = send.MarkFlagRequired("faxline")
	_ = send.MarkFlagRequired("to")
	_ = send.MarkFlagRequired("file")
	cmd.AddCommand(send)
	return cmd
}
