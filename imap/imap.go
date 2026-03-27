package imap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	imaplib "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"

	"github.com/kungfusheep/mail/provider"
)

type Config struct {
	Server     string `json:"server"`
	SMTPServer string `json:"smtp_server"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

type IMAP struct {
	config Config
	client *imapclient.Client
}

func LoadConfig() (Config, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "mail", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w\ncreate it with: {\"server\": \"imap.gmail.com:993\", \"email\": \"you@gmail.com\", \"password\": \"your-app-password\"}", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Server == "" {
		cfg.Server = "imap.gmail.com:993"
	}
	if cfg.SMTPServer == "" {
		cfg.SMTPServer = "smtp.gmail.com:587"
	}
	return cfg, nil
}

func New(cfg Config) *IMAP {
	return &IMAP{config: cfg}
}

func (im *IMAP) Authenticate() error {
	c, err := imapclient.DialTLS(im.config.Server, nil)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", im.config.Server, err)
	}
	im.client = c

	if err := c.Login(im.config.Email, im.config.Password).Wait(); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	return nil
}

func (im *IMAP) ListFolders() ([]provider.Folder, error) {
	mailboxes, err := im.client.List("", "*", &imaplib.ListOptions{
		ReturnStatus: &imaplib.StatusOptions{
			NumMessages: true,
			NumUnseen:   true,
		},
	}).Collect()
	if err != nil {
		return nil, err
	}

	var folders []provider.Folder
	for _, mb := range mailboxes {
		name := mb.Mailbox
		// gmail uses [Gmail]/Sent Mail etc. — simplify
		if strings.HasPrefix(name, "[Gmail]/") {
			name = strings.TrimPrefix(name, "[Gmail]/")
		}

		f := provider.Folder{
			ID:   mb.Mailbox,
			Name: name,
		}
		if mb.Status != nil {
			if mb.Status.NumMessages != nil {
				f.Total = int(*mb.Status.NumMessages)
			}
			if mb.Status.NumUnseen != nil {
				f.Unread = int(*mb.Status.NumUnseen)
			}
		}
		folders = append(folders, f)
	}
	return folders, nil
}

func (im *IMAP) ListThreads(opts provider.ListOptions) (provider.ListResult, error) {
	folder := opts.Folder
	if folder == "" {
		folder = "INBOX"
	}

	mbox, err := im.client.Select(folder, nil).Wait()
	if err != nil {
		return provider.ListResult{}, fmt.Errorf("selecting %s: %w", folder, err)
	}

	if mbox.NumMessages == 0 {
		return provider.ListResult{}, nil
	}

	// fetch the most recent messages
	count := uint32(25)
	if opts.MaxResults > 0 {
		count = uint32(opts.MaxResults)
	}
	if count > mbox.NumMessages {
		count = mbox.NumMessages
	}

	// fetch from newest to oldest
	from := mbox.NumMessages - count + 1
	var seqSet imaplib.SeqSet
	seqSet.AddRange(from, mbox.NumMessages)

	fetchOpts := &imaplib.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
	}

	messages, err := im.client.Fetch(seqSet, fetchOpts).Collect()
	if err != nil {
		return provider.ListResult{}, fmt.Errorf("fetching messages: %w", err)
	}

	// group by gmail thread (X-GM-THRID) or by subject for non-gmail
	// for now, each message is its own "thread" — we can group later
	var threads []provider.Thread
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Envelope == nil {
			continue
		}

		pm := envelopeToMessage(msg)
		unread := 0
		if !pm.Read {
			unread = 1
		}

		threads = append(threads, provider.Thread{
			ID:           fmt.Sprintf("%d", msg.UID),
			Subject:      pm.Subject,
			Snippet:      "",
			Messages:     []provider.Message{pm},
			Unread:       unread,
			Date:         pm.Date,
			Participants: []provider.Address{pm.From},
		})
	}

	return provider.ListResult{Threads: threads}, nil
}

func (im *IMAP) GetThread(id string) (provider.Thread, error) {
	// for now threads are single messages — just fetch the message
	msg, err := im.GetMessage(id)
	if err != nil {
		return provider.Thread{}, err
	}
	return provider.Thread{
		ID:           id,
		Subject:      msg.Subject,
		Messages:     []provider.Message{msg},
		Date:         msg.Date,
		Participants: []provider.Address{msg.From},
	}, nil
}

func (im *IMAP) GetMessage(id string) (provider.Message, error) {
	var uid imaplib.UID
	fmt.Sscanf(id, "%d", &uid)

	uidSet := imaplib.UIDSetNum(uid)
	bodySection := &imaplib.FetchItemBodySection{Peek: true}
	fetchOpts := &imaplib.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
		BodySection: []*imaplib.FetchItemBodySection{bodySection},
	}

	messages, err := im.client.Fetch(uidSet, fetchOpts).Collect()
	if err != nil {
		return provider.Message{}, err
	}
	if len(messages) == 0 {
		return provider.Message{}, fmt.Errorf("message %s not found", id)
	}

	msg := messages[0]
	pm := envelopeToMessage(msg)

	// parse body
	bodyBytes := msg.FindBodySection(bodySection)
	if bodyBytes != nil {
		text, html := parseBody(bodyBytes)
		pm.TextBody = text
		pm.HTMLBody = html
	}

	return pm, nil
}

func (im *IMAP) Send(msg provider.Message) error {
	// IMAP is read-only — sending needs SMTP
	return fmt.Errorf("send not implemented — needs SMTP")
}

func (im *IMAP) Reply(threadID string, msg provider.Message) error {
	return im.Send(msg)
}

func (im *IMAP) Move(messageIDs []string, folderID string) error {
	for _, id := range messageIDs {
		var uid imaplib.UID
		fmt.Sscanf(id, "%d", &uid)
		uidSet := imaplib.UIDSetNum(uid)
		if _, err := im.client.Move(uidSet, folderID).Wait(); err != nil {
			return err
		}
	}
	return nil
}

func (im *IMAP) Archive(messageIDs []string) error {
	return im.Move(messageIDs, "[Gmail]/All Mail")
}

func (im *IMAP) Delete(messageIDs []string) error {
	return im.Move(messageIDs, "[Gmail]/Trash")
}

func (im *IMAP) MarkRead(messageIDs []string, read bool) error {
	for _, id := range messageIDs {
		var uid imaplib.UID
		fmt.Sscanf(id, "%d", &uid)
		uidSet := imaplib.UIDSetNum(uid)

		var flags imaplib.StoreFlags
		if read {
			flags.Op = imaplib.StoreFlagsAdd
		} else {
			flags.Op = imaplib.StoreFlagsDel
		}
		flags.Flags = []imaplib.Flag{imaplib.FlagSeen}

		if err := im.client.Store(uidSet, &flags, nil).Close(); err != nil {
			return err
		}
	}
	return nil
}

func (im *IMAP) Star(messageIDs []string, starred bool) error {
	for _, id := range messageIDs {
		var uid imaplib.UID
		fmt.Sscanf(id, "%d", &uid)
		uidSet := imaplib.UIDSetNum(uid)

		var flags imaplib.StoreFlags
		if starred {
			flags.Op = imaplib.StoreFlagsAdd
		} else {
			flags.Op = imaplib.StoreFlagsDel
		}
		flags.Flags = []imaplib.Flag{imaplib.FlagFlagged}

		if err := im.client.Store(uidSet, &flags, nil).Close(); err != nil {
			return err
		}
	}
	return nil
}

func (im *IMAP) Search(query string, maxResults int) ([]provider.Thread, error) {
	criteria := &imaplib.SearchCriteria{
		Text: []string{query},
	}
	data, err := im.client.Search(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	// use the search results directly as a NumSet for fetch
	if data.All == nil {
		return nil, nil
	}

	messages, err := im.client.Fetch(data.All, &imaplib.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
	}).Collect()
	if err != nil {
		return nil, err
	}

	var threads []provider.Thread
	for _, msg := range messages {
		if msg.Envelope == nil {
			continue
		}
		pm := envelopeToMessage(msg)
		threads = append(threads, provider.Thread{
			ID:       fmt.Sprintf("%d", msg.UID),
			Subject:  pm.Subject,
			Messages: []provider.Message{pm},
			Date:     pm.Date,
		})
	}
	return threads, nil
}

func (im *IMAP) Close() error {
	if im.client != nil {
		return im.client.Close()
	}
	return nil
}

// helpers

func envelopeToMessage(msg *imapclient.FetchMessageBuffer) provider.Message {
	env := msg.Envelope

	pm := provider.Message{
		ID:        fmt.Sprintf("%d", msg.UID),
		Subject:   env.Subject,
		Date:      env.Date,
		MessageID: env.MessageID,
		Read:      hasFlag(msg.Flags, imaplib.FlagSeen),
		Starred:   hasFlag(msg.Flags, imaplib.FlagFlagged),
	}

	if len(env.From) > 0 {
		pm.From = convertAddress(env.From[0])
	}
	for _, a := range env.To {
		pm.To = append(pm.To, convertAddress(a))
	}
	for _, a := range env.Cc {
		pm.CC = append(pm.CC, convertAddress(a))
	}
	if len(env.InReplyTo) > 0 {
		pm.InReplyTo = env.InReplyTo[0]
	}

	return pm
}

func convertAddress(a imaplib.Address) provider.Address {
	return provider.Address{
		Name:  a.Name,
		Email: a.Addr(),
	}
}

func hasFlag(flags []imaplib.Flag, target imaplib.Flag) bool {
	for _, f := range flags {
		if f == target {
			return true
		}
	}
	return false
}

func parseBody(data []byte) (text, html string) {
	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		return string(data), ""
	}

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := p.Header.(*mail.InlineHeader).ContentType()
			body, err := io.ReadAll(p.Body)
			if err != nil {
				continue
			}
			switch ct {
			case "text/plain":
				if text == "" {
					text = string(body)
				}
			case "text/html":
				if html == "" {
					html = string(body)
				}
			}
		}
	}

	return text, html
}
