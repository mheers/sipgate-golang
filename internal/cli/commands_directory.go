package cli

import (
	"errors"
	"strconv"

	"github.com/mheers/sipgate-golang/openapi"
	"github.com/spf13/cobra"
)

type contactFlags struct {
	name, given, family, note, scope string
	emails, phones                   []string
}

func (f *contactFlags) createBody() openapi.CreateContactRequest {
	body := openapi.CreateContactRequest{}
	if f.name != "" {
		body.Name = &f.name
	}
	if f.given != "" {
		body.Given = &f.given
	}
	if f.family != "" {
		body.Family = &f.family
	}
	if f.note != "" {
		body.Note = &f.note
	}
	if f.scope != "" {
		body.Scope = &f.scope
	}
	for _, email := range f.emails {
		value := email
		body.Emails = append(body.Emails, openapi.EmailRequest{Email: &value})
	}
	for _, phone := range f.phones {
		value := phone
		body.Numbers = append(body.Numbers, openapi.NumberRequest{Number: &value})
	}
	return body
}

func (f *contactFlags) updateBody(id string) openapi.UpdateContactRequest {
	body := openapi.UpdateContactRequest{Id: &id}
	if f.name != "" {
		body.Name = &f.name
	}
	if f.given != "" {
		body.Given = &f.given
	}
	if f.family != "" {
		body.Family = &f.family
	}
	if f.note != "" {
		body.Note = &f.note
	}
	if f.scope != "" {
		body.Scope = &f.scope
	}
	for _, email := range f.emails {
		value := email
		body.Emails = append(body.Emails, openapi.EmailRequest{Email: &value})
	}
	for _, phone := range f.phones {
		value := phone
		body.Numbers = append(body.Numbers, openapi.NumberRequest{Number: &value})
	}
	return body
}

func (f *contactFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.name, "name", "", "display name")
	flags.StringVar(&f.given, "given", "", "given name")
	flags.StringVar(&f.family, "family", "", "family name")
	flags.StringVar(&f.note, "note", "", "note")
	flags.StringVar(&f.scope, "scope", "", "scope: PRIVATE, SHARED, INTERNAL or PRIVATE,SHARED")
	flags.StringArrayVar(&f.emails, "email", nil, "email address, repeatable")
	flags.StringArrayVar(&f.phones, "phone", nil, "phone number, repeatable")
}

func (a *App) contactsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contacts",
		Short: "Manage contacts",
	}

	var (
		limit, offset int
		scopes        []string
		phoneNumbers  []string
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List contacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 || offset < 0 {
				return errors.New("--limit and --offset must not be negative")
			}
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			request := client.ContactsAPI.GetContacts1(cmd.Context()).
				Limit(int32(limit)).
				Offset(int32(offset))
			if len(scopes) > 0 {
				request = request.Scopes(scopes)
			}
			if len(phoneNumbers) > 0 {
				request = request.Phonenumbers(phoneNumbers)
			}
			contacts, err := result(request.Execute())
			if err != nil {
				return err
			}
			return a.print(contacts)
		},
	}
	list.Flags().IntVar(&limit, "limit", 100, "maximum number of contacts")
	list.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	list.Flags().StringSliceVar(&scopes, "scope", nil, "filter by scope")
	list.Flags().StringSliceVar(&phoneNumbers, "number", nil, "filter by phone number")

	get := &cobra.Command{
		Use:   "get <contactId>",
		Short: "Show a contact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			contact, err := result(client.ContactsAPI.GetContactById(cmd.Context(), args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(contact)
		},
	}

	var createFlags contactFlags
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a contact",
		RunE: func(cmd *cobra.Command, args []string) error {
			if createFlags.name == "" && (createFlags.given == "" || createFlags.family == "") {
				return errors.New("provide --name or both --given and --family")
			}
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := check(client.ContactsAPI.CreateContact(cmd.Context()).Body(createFlags.createBody()).Execute()); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "created"})
		},
	}
	createFlags.register(create)

	var updateFlags contactFlags
	update := &cobra.Command{
		Use:   "update <contactId>",
		Short: "Update a contact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := check(client.ContactsAPI.UpdateContact(cmd.Context(), args[0]).
				Body(updateFlags.updateBody(args[0])).Execute()); err != nil {
				return err
			}
			return a.print(map[string]any{"status": "updated", "contactId": args[0]})
		},
	}
	updateFlags.register(update)

	deleteCmd := &cobra.Command{
		Use:   "delete <contactId>",
		Short: "Delete a contact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := result(client.ContactsAPI.DeleteContact(cmd.Context(), args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(deleted)
		},
	}

	cmd.AddCommand(list, get, create, update, deleteCmd)
	return cmd
}

func (a *App) addressesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "addresses",
		Short: "Inspect addresses",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List addresses",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			addresses, err := result(client.AddressesAPI.GetAddresses(cmd.Context()).Execute())
			if err != nil {
				return err
			}
			return a.print(addresses)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "get <addressId>",
		Short: "Show an address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 32)
			if err != nil {
				return errors.New("addressId must be a number")
			}
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			address, err := result(client.AddressesAPI.GetAddress(cmd.Context(), int32(id)).Execute())
			if err != nil {
				return err
			}
			return a.print(address)
		},
	})
	return cmd
}

func (a *App) devicesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "Inspect devices",
	}
	var userID, deviceType string
	list := &cobra.Command{
		Use:   "list",
		Short: "List devices of a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			request := client.DevicesAPI.GetDevices(cmd.Context(), userID)
			if deviceType != "" {
				request = request.Type_(deviceType)
			}
			devices, err := result(request.Execute())
			if err != nil {
				return err
			}
			return a.print(devices)
		},
	}
	list.Flags().StringVar(&userID, "user", "w0", "user ID")
	list.Flags().StringVar(&deviceType, "type", "", "filter by device type")

	cmd.AddCommand(list, &cobra.Command{
		Use:   "get <deviceId>",
		Short: "Show a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			device, err := result(client.DevicesAPI.GetDevice(cmd.Context(), args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(device)
		},
	})
	return cmd
}

func (a *App) numbersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "numbers",
		Short: "Inspect phone numbers",
	}
	var userID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List phone numbers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			var numbers any
			if userID == "" {
				value, err := result(client.NumbersAPI.GetNumbers(cmd.Context()).Execute())
				if err != nil {
					return err
				}
				numbers = value
			} else {
				value, err := result(client.NumbersAPI.GetUserNumbers(cmd.Context(), userID).Execute())
				if err != nil {
					return err
				}
				numbers = value
			}
			return a.print(numbers)
		},
	}
	list.Flags().StringVar(&userID, "user", "", "only show numbers of this user")
	cmd.AddCommand(list)
	return cmd
}

func (a *App) phonelinesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "phonelines",
		Short: "Inspect phone lines",
	}
	var userID string
	list := &cobra.Command{
		Use:   "list",
		Short: "List phone lines",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			if userID == "" {
				phonelines, err := result(client.PhonelinesAPI.GetAllPhonelines(cmd.Context()).Execute())
				if err != nil {
					return err
				}
				return a.print(phonelines)
			}
			phonelines, err := result(client.PhonelinesAPI.GetPhonelines(cmd.Context(), userID).Execute())
			if err != nil {
				return err
			}
			return a.print(phonelines)
		},
	}
	list.Flags().StringVar(&userID, "user", "", "only show phone lines of this user")

	var getPhonelineUser string
	get := &cobra.Command{
		Use:   "get <phonelineId>",
		Short: "Show a phone line",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			phoneline, err := result(client.PhonelinesAPI.GetPhoneline(cmd.Context(), getPhonelineUser, args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(phoneline)
		},
	}
	get.Flags().StringVar(&getPhonelineUser, "user", "w0", "user ID")
	cmd.AddCommand(list, get)
	return cmd
}

func (a *App) usersCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Inspect users",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List users",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			users, err := result(client.UsersAPI.GetAllUsers(cmd.Context()).Execute())
			if err != nil {
				return err
			}
			return a.print(users)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "get <userId>",
		Short: "Show a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			user, err := result(client.UsersAPI.GetUser(cmd.Context(), args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(user)
		},
	})
	return cmd
}

func (a *App) voicemailsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "voicemails",
		Short: "Inspect voicemails",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List voicemails",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			voicemails, err := result(client.VoicemailsAPI.GetVoicemails(cmd.Context()).Execute())
			if err != nil {
				return err
			}
			return a.print(voicemails)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "get <voicemailId>",
		Short: "Show a voicemail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.newClient(cmd.Context())
			if err != nil {
				return err
			}
			voicemail, err := result(client.VoicemailsAPI.GetVoicemail(cmd.Context(), args[0]).Execute())
			if err != nil {
				return err
			}
			return a.print(voicemail)
		},
	})
	return cmd
}
