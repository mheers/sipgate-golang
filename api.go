package sipgate

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mheers/sipgate-golang/openapi"
)

// SMS describes an SMS to send through a sipgate SMS extension.
type SMS struct {
	// SMSID is the ID of the SMS extension, for example "s0".
	SMSID string
	// Recipient is the destination number in E.164 format, for example
	// "+4915799912345".
	Recipient string
	// Message is the message text.
	Message string
	// SendAt schedules the message. The zero value sends immediately.
	SendAt time.Time
}

// CallRequest describes an outgoing call.
type CallRequest struct {
	// Caller is the ID of the calling extension, for example "e0".
	Caller string
	// Callee is the destination number in E.164 format.
	Callee string
	// CallerID is the number presented to the callee.
	CallerID string
	// DeviceID optionally routes the call to a specific device.
	DeviceID string
}

// Account returns the account data of the authenticated user.
func (c *Client) Account(ctx context.Context) (*openapi.AccountResponse, error) {
	account, resp, err := c.AccountAPI.GetAccount(ctx).Execute()
	if err != nil {
		return nil, wrapError(resp, err)
	}
	return account, nil
}

// Balance returns the current account balance.
func (c *Client) Balance(ctx context.Context) (*openapi.BalanceResponse, error) {
	balance, resp, err := c.AccountAPI.GetBalance(ctx).Execute()
	if err != nil {
		return nil, wrapError(resp, err)
	}
	return balance, nil
}

// Userinfo returns information about the logged in user.
func (c *Client) Userinfo(ctx context.Context) (*openapi.UserinfoResponse, error) {
	info, resp, err := c.AuthorizationAPI.Userinfo(ctx).Execute()
	if err != nil {
		return nil, wrapError(resp, err)
	}
	return info, nil
}

// SendSMS sends an SMS through the given SMS extension.
func (c *Client) SendSMS(ctx context.Context, sms SMS) error {
	if err := sms.validate(); err != nil {
		return err
	}

	body := openapi.SendWebSmsRequest{
		Message:   sms.Message,
		Recipient: sms.Recipient,
		SmsId:     sms.SMSID,
	}
	if !sms.SendAt.IsZero() {
		sendAt := sms.SendAt.Unix()
		body.SendAt = &sendAt
	}

	resp, err := c.SessionsAPI.SendWebSms(ctx).Body(body).Execute()
	return wrapError(resp, err)
}

func (s SMS) validate() error {
	if strings.TrimSpace(s.SMSID) == "" {
		return errors.New("sipgate: SMSID is required")
	}
	if strings.TrimSpace(s.Recipient) == "" {
		return errors.New("sipgate: recipient is required")
	}
	if s.Message == "" {
		return errors.New("sipgate: message is required")
	}
	return nil
}

// CreateCall starts an outgoing call and returns the sipgate session ID.
func (c *Client) CreateCall(ctx context.Context, call CallRequest) (string, error) {
	if strings.TrimSpace(call.Caller) == "" {
		return "", errors.New("sipgate: caller is required")
	}
	if strings.TrimSpace(call.Callee) == "" {
		return "", errors.New("sipgate: callee is required")
	}

	body := openapi.NewCallRequest{
		Caller: call.Caller,
		Callee: call.Callee,
	}
	if call.CallerID != "" {
		body.CallerId = &call.CallerID
	}
	if call.DeviceID != "" {
		body.DeviceId = &call.DeviceID
	}

	session, resp, err := c.SessionsAPI.NewCall(ctx).Body(body).Execute()
	if err != nil {
		return "", wrapError(resp, err)
	}
	if session == nil || session.GetSessionId() == "" {
		return "", errors.New("sipgate: call was created without a session ID")
	}
	return session.GetSessionId(), nil
}

// Calls returns all currently established calls.
func (c *Client) Calls(ctx context.Context) ([]openapi.Call, error) {
	calls, resp, err := c.RtcmAPI.GetCalls(ctx).Execute()
	if err != nil {
		return nil, wrapError(resp, err)
	}
	if calls == nil {
		return nil, nil
	}
	return calls.Data, nil
}

// HangupCall terminates an established call.
func (c *Client) HangupCall(ctx context.Context, callID string) error {
	if strings.TrimSpace(callID) == "" {
		return errors.New("sipgate: call ID is required")
	}
	resp, err := c.RtcmAPI.HangupCall(ctx, callID).Execute()
	return wrapError(resp, err)
}
