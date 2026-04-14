package imap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
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

func (im *IMAP) reconnect() error {
	if im.client != nil {
		im.client.Close()
		im.client = nil
	}
	log.Println("imap: reconnecting...")
	if err := im.Authenticate(); err != nil {
		return err
	}
	log.Println("imap: reconnected")
	return nil
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "EOF")
}

func withRetry[T any](im *IMAP, fn func() (T, error)) (T, error) {
	v, err := fn()
	if err == nil || !isConnectionError(err) {
		return v, err
	}
	if rerr := im.reconnect(); rerr != nil {
		var zero T
		return zero, fmt.Errorf("%v (reconnect also failed: %v)", err, rerr)
	}
	return fn()
}

func (im *IMAP) ListFolders() ([]provider.Folder, error) {
	return withRetry(im, func() ([]provider.Folder, error) {
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
			if strings.HasPrefix(name, "[Google Mail]/") {
				name = strings.TrimPrefix(name, "[Google Mail]/")
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
	})
}

func (im *IMAP) ListThreads(opts provider.ListOptions) (provider.ListResult, error) {
	return withRetry(im, func() (provider.ListResult, error) {
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

		count := uint32(25)
		if opts.MaxResults > 0 {
			count = uint32(opts.MaxResults)
		}
		if count > mbox.NumMessages {
			count = mbox.NumMessages
		}

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

		var allMsgs []provider.Message
		for _, msg := range messages {
			if msg.Envelope == nil {
				continue
			}
			allMsgs = append(allMsgs, envelopeToMessage(msg))
		}

		threads := groupIntoThreads(allMsgs)

		sort.Slice(threads, func(i, j int) bool {
			return threads[i].Date.After(threads[j].Date)
		})

		return provider.ListResult{Threads: threads}, nil
	})
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
	return withRetry(im, func() (provider.Message, error) {
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

		bodyBytes := msg.FindBodySection(bodySection)
		if bodyBytes != nil {
			text, html := parseBody(bodyBytes)
			pm.TextBody = text
			pm.HTMLBody = html
		}

		return pm, nil
	})
}

func (im *IMAP) Send(msg provider.Message) error {
	// IMAP is read-only — sending needs SMTP
	return fmt.Errorf("send not implemented — needs SMTP")
}

func (im *IMAP) Reply(threadID string, msg provider.Message) error {
	return im.Send(msg)
}

func (im *IMAP) Move(messageIDs []string, folderID string) error {
	_, err := withRetry(im, func() (struct{}, error) {
		for _, id := range messageIDs {
			var uid imaplib.UID
			fmt.Sscanf(id, "%d", &uid)
			uidSet := imaplib.UIDSetNum(uid)
			if _, err := im.client.Move(uidSet, folderID).Wait(); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (im *IMAP) MarkRead(messageIDs []string, read bool) error {
	_, err := withRetry(im, func() (struct{}, error) {
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
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (im *IMAP) Star(messageIDs []string, starred bool) error {
	_, err := withRetry(im, func() (struct{}, error) {
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
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (im *IMAP) Search(query string, maxResults int) ([]provider.Thread, error) {
	return withRetry(im, func() ([]provider.Thread, error) {
		criteria := &imaplib.SearchCriteria{
			Text: []string{query},
		}
		data, err := im.client.Search(criteria, nil).Wait()
		if err != nil {
			return nil, err
		}

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
	})
}

func (im *IMAP) Close() error {
	if im.client != nil {
		return im.client.Close()
	}
	return nil
}

// resolveReferences finds messages referenced by InReplyTo that aren't in the
// current set. Searches All Mail for the missing messages and adds them.


// threading — group messages into conversations using InReplyTo/MessageID

func groupIntoThreads(msgs []provider.Message) []provider.Thread {
	// if X-GM-THRID is available, use it directly — no heuristics needed
	hasGmailThreads := false
	for _, m := range msgs {
		if m.ThreadID != "" {
			hasGmailThreads = true
			break
		}
	}
	if hasGmailThreads {
		log.Printf("threading: using X-GM-THRID for %d messages", len(msgs))
		return groupByGmailThread(msgs)
	}

	for _, m := range msgs {
		if m.InReplyTo != "" || m.MessageID != "" {
			log.Printf("threading: uid=%s subj=%q msgid=%q inreplyto=%q", m.ID, m.Subject, m.MessageID, m.InReplyTo)
		}
	}

	// build lookup: MessageID → message
	byMsgID := make(map[string]*provider.Message, len(msgs))
	for i := range msgs {
		if msgs[i].MessageID != "" {
			byMsgID[msgs[i].MessageID] = &msgs[i]
		}
	}

	// union-find: map each MessageID to its root thread ID
	parent := make(map[string]string)

	var find func(string) string
	find = func(id string) string {
		p, ok := parent[id]
		if !ok {
			parent[id] = id
			return id
		}
		if p != id {
			parent[id] = find(p)
		}
		return parent[id]
	}

	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// link messages to their parents via InReplyTo
	for i := range msgs {
		m := &msgs[i]
		if m.MessageID != "" {
			find(m.MessageID)
		}
		if m.InReplyTo != "" && m.MessageID != "" {
			union(m.MessageID, m.InReplyTo)
		}
	}

	// fallback: group by normalized subject when InReplyTo doesn't resolve
	bySubject := make(map[string]string) // normalized subject → first MessageID
	for i := range msgs {
		m := &msgs[i]
		if m.MessageID == "" {
			continue
		}
		norm := normalizeSubject(m.Subject)
		if existing, ok := bySubject[norm]; ok {
			union(m.MessageID, existing)
		} else {
			bySubject[norm] = m.MessageID
		}
	}

	// group messages by root
	groups := make(map[string][]provider.Message)
	for _, m := range msgs {
		var root string
		if m.MessageID != "" {
			root = find(m.MessageID)
		} else {
			root = m.ID // no message-id, standalone
		}
		groups[root] = append(groups[root], m)
	}

	// build threads
	var threads []provider.Thread
	for _, group := range groups {
		// sort messages chronologically within thread
		sort.Slice(group, func(i, j int) bool {
			return group[i].Date.Before(group[j].Date)
		})

		// collect participants (deduplicate)
		seen := make(map[string]bool)
		var participants []provider.Address
		unread := 0
		for _, m := range group {
			if !m.Read {
				unread++
			}
			key := m.From.Email
			if !seen[key] {
				seen[key] = true
				participants = append(participants, m.From)
			}
		}

		latest := group[len(group)-1]
		threads = append(threads, provider.Thread{
			ID:           latest.ID,
			Subject:      group[0].Subject,
			Messages:     group,
			Unread:       unread,
			Date:         latest.Date,
			Participants: participants,
		})
	}

	log.Printf("threading: %d messages → %d threads", len(msgs), len(threads))
	for _, t := range threads {
		if len(t.Messages) > 1 {
			log.Printf("threading: thread %q has %d messages", t.Subject, len(t.Messages))
		}
	}

	return threads
}

func groupByGmailThread(msgs []provider.Message) []provider.Thread {
	groups := make(map[string][]provider.Message)
	for _, m := range msgs {
		tid := m.ThreadID
		if tid == "" {
			tid = m.ID // fallback for messages without thread ID
		}
		groups[tid] = append(groups[tid], m)
	}

	var threads []provider.Thread
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			return group[i].Date.Before(group[j].Date)
		})

		seen := make(map[string]bool)
		var participants []provider.Address
		unread := 0
		for _, m := range group {
			if !m.Read {
				unread++
			}
			if !seen[m.From.Email] {
				seen[m.From.Email] = true
				participants = append(participants, m.From)
			}
		}

		latest := group[len(group)-1]
		threads = append(threads, provider.Thread{
			ID:           latest.ID,
			Subject:      group[0].Subject,
			Messages:     group,
			Unread:       unread,
			Date:         latest.Date,
			Participants: participants,
		})
	}
	return threads
}

func normalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		lower := strings.ToLower(s)
		if strings.HasPrefix(lower, "re:") {
			s = strings.TrimSpace(s[3:])
		} else if strings.HasPrefix(lower, "fwd:") {
			s = strings.TrimSpace(s[4:])
		} else if strings.HasPrefix(lower, "fw:") {
			s = strings.TrimSpace(s[3:])
		} else {
			break
		}
	}
	return strings.ToLower(s)
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
