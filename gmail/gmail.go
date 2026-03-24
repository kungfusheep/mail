package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kungfusheep/mail/provider"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Gmail struct {
	svc    *gmail.Service
	config *oauth2.Config
	user   string
}

func New(credentialsPath string) (*Gmail, error) {
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}

	config, err := google.ConfigFromJSON(data,
		gmail.GmailReadonlyScope,
		gmail.GmailSendScope,
		gmail.GmailModifyScope,
	)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}

	return &Gmail{
		config: config,
		user:   "me",
	}, nil
}

func (g *Gmail) Authenticate() error {
	tok, err := g.loadToken()
	if err != nil {
		tok, err = g.authFlow()
		if err != nil {
			return fmt.Errorf("auth flow: %w", err)
		}
		if err := g.saveToken(tok); err != nil {
			return fmt.Errorf("saving token: %w", err)
		}
	}

	client := g.config.Client(context.Background(), tok)
	svc, err := gmail.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("creating gmail service: %w", err)
	}
	g.svc = svc
	return nil
}

func (g *Gmail) ListFolders() ([]provider.Folder, error) {
	res, err := g.svc.Users.Labels.List(g.user).Do()
	if err != nil {
		return nil, err
	}

	var folders []provider.Folder
	for _, l := range res.Labels {
		// skip internal labels that aren't useful in a folder view
		if l.Type == "system" && isHiddenLabel(l.Id) {
			continue
		}
		detail, err := g.svc.Users.Labels.Get(g.user, l.Id).Do()
		if err != nil {
			continue
		}
		folders = append(folders, provider.Folder{
			ID:     l.Id,
			Name:   labelDisplayName(l.Name),
			Unread: int(detail.MessagesUnread),
			Total:  int(detail.MessagesTotal),
		})
	}

	sort.Slice(folders, func(i, j int) bool {
		return labelOrder(folders[i].ID) < labelOrder(folders[j].ID)
	})

	return folders, nil
}

func (g *Gmail) ListThreads(opts provider.ListOptions) (provider.ListResult, error) {
	call := g.svc.Users.Threads.List(g.user)
	if opts.Folder != "" {
		call = call.LabelIds(opts.Folder)
	}
	if opts.Query != "" {
		call = call.Q(opts.Query)
	}
	if opts.PageToken != "" {
		call = call.PageToken(opts.PageToken)
	}
	max := opts.MaxResults
	if max <= 0 {
		max = 25
	}
	call = call.MaxResults(int64(max))

	res, err := call.Do()
	if err != nil {
		return provider.ListResult{}, err
	}

	var threads []provider.Thread
	for _, t := range res.Threads {
		thread, err := g.GetThread(t.Id)
		if err != nil {
			continue
		}
		threads = append(threads, thread)
	}

	return provider.ListResult{
		Threads:       threads,
		NextPageToken: res.NextPageToken,
	}, nil
}

func (g *Gmail) GetThread(id string) (provider.Thread, error) {
	t, err := g.svc.Users.Threads.Get(g.user, id).Format("full").Do()
	if err != nil {
		return provider.Thread{}, err
	}

	var msgs []provider.Message
	participants := map[string]provider.Address{}
	var latest time.Time

	for _, m := range t.Messages {
		msg := gmailMessageToProvider(m)
		msgs = append(msgs, msg)
		if msg.Date.After(latest) {
			latest = msg.Date
		}
		participants[msg.From.Email] = msg.From
	}

	var unread int
	for _, m := range msgs {
		if !m.Read {
			unread++
		}
	}

	var parts []provider.Address
	for _, a := range participants {
		parts = append(parts, a)
	}

	subject := ""
	if len(msgs) > 0 {
		subject = msgs[0].Subject
	}
	snippet := ""
	if len(msgs) > 0 {
		snippet = t.Snippet
	}

	return provider.Thread{
		ID:           id,
		Subject:      subject,
		Snippet:      snippet,
		Messages:     msgs,
		Unread:       unread,
		Date:         latest,
		Participants: parts,
	}, nil
}

func (g *Gmail) GetMessage(id string) (provider.Message, error) {
	m, err := g.svc.Users.Messages.Get(g.user, id).Format("full").Do()
	if err != nil {
		return provider.Message{}, err
	}
	return gmailMessageToProvider(m), nil
}

func (g *Gmail) Send(msg provider.Message) error {
	raw := buildRawMessage(msg)
	_, err := g.svc.Users.Messages.Send(g.user, &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString([]byte(raw)),
	}).Do()
	return err
}

func (g *Gmail) Reply(threadID string, msg provider.Message) error {
	raw := buildRawMessage(msg)
	_, err := g.svc.Users.Messages.Send(g.user, &gmail.Message{
		ThreadId: threadID,
		Raw:      base64.URLEncoding.EncodeToString([]byte(raw)),
	}).Do()
	return err
}

func (g *Gmail) Move(messageIDs []string, folderID string) error {
	for _, id := range messageIDs {
		// get current labels to remove them
		m, err := g.svc.Users.Messages.Get(g.user, id).Format("minimal").Do()
		if err != nil {
			return err
		}
		var remove []string
		for _, l := range m.LabelIds {
			if l != "UNREAD" && l != "STARRED" {
				remove = append(remove, l)
			}
		}
		_, err = g.svc.Users.Messages.Modify(g.user, id, &gmail.ModifyMessageRequest{
			AddLabelIds:    []string{folderID},
			RemoveLabelIds: remove,
		}).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Gmail) Archive(messageIDs []string) error {
	for _, id := range messageIDs {
		_, err := g.svc.Users.Messages.Modify(g.user, id, &gmail.ModifyMessageRequest{
			RemoveLabelIds: []string{"INBOX"},
		}).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Gmail) Delete(messageIDs []string) error {
	for _, id := range messageIDs {
		_, err := g.svc.Users.Messages.Trash(g.user, id).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Gmail) MarkRead(messageIDs []string, read bool) error {
	for _, id := range messageIDs {
		mod := &gmail.ModifyMessageRequest{}
		if read {
			mod.RemoveLabelIds = []string{"UNREAD"}
		} else {
			mod.AddLabelIds = []string{"UNREAD"}
		}
		_, err := g.svc.Users.Messages.Modify(g.user, id, mod).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Gmail) Star(messageIDs []string, starred bool) error {
	for _, id := range messageIDs {
		mod := &gmail.ModifyMessageRequest{}
		if starred {
			mod.AddLabelIds = []string{"STARRED"}
		} else {
			mod.RemoveLabelIds = []string{"STARRED"}
		}
		_, err := g.svc.Users.Messages.Modify(g.user, id, mod).Do()
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Gmail) Search(query string, maxResults int) ([]provider.Thread, error) {
	res, err := g.ListThreads(provider.ListOptions{
		Query:      query,
		MaxResults: maxResults,
	})
	if err != nil {
		return nil, err
	}
	return res.Threads, nil
}

// oauth helpers

func (g *Gmail) tokenPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "mail")
	os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "token.json")
}

func (g *Gmail) loadToken() (*oauth2.Token, error) {
	data, err := os.ReadFile(g.tokenPath())
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func (g *Gmail) saveToken(tok *oauth2.Token) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(g.tokenPath(), data, 0600)
}

// browser-based oauth flow with local redirect
func (g *Gmail) authFlow() (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	g.config.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			fmt.Fprint(w, "error: no code received")
			return
		}
		codeCh <- code
		fmt.Fprint(w, "authenticated! you can close this tab.")
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)

	url := g.config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("open this url to authenticate:\n%s\n", url)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-time.After(2 * time.Minute):
		return nil, fmt.Errorf("authentication timed out")
	}

	srv.Shutdown(context.Background())

	tok, err := g.config.Exchange(context.Background(), code)
	if err != nil {
		return nil, fmt.Errorf("exchanging code: %w", err)
	}
	return tok, nil
}

// gmail to provider type conversion

func gmailMessageToProvider(m *gmail.Message) provider.Message {
	headers := map[string]string{}
	if m.Payload != nil {
		for _, h := range m.Payload.Headers {
			headers[strings.ToLower(h.Name)] = h.Value
		}
	}

	msg := provider.Message{
		ID:        m.Id,
		ThreadID:  m.ThreadId,
		From:      parseAddress(headers["from"]),
		To:        parseAddresses(headers["to"]),
		CC:        parseAddresses(headers["cc"]),
		Subject:   headers["subject"],
		Date:      parseDate(headers["date"]),
		Read:      !hasLabel(m.LabelIds, "UNREAD"),
		Starred:   hasLabel(m.LabelIds, "STARRED"),
		Labels:    m.LabelIds,
		MessageID: headers["message-id"],
		InReplyTo: headers["in-reply-to"],
	}

	if refs := headers["references"]; refs != "" {
		msg.References = strings.Fields(refs)
	}

	if m.Payload != nil {
		msg.TextBody, msg.HTMLBody = extractBodies(m.Payload)
	}

	return msg
}

func extractBodies(part *gmail.MessagePart) (text, html string) {
	if part.MimeType == "text/plain" && part.Body != nil && part.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(part.Body.Data)
		text = string(decoded)
	}
	if part.MimeType == "text/html" && part.Body != nil && part.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(part.Body.Data)
		html = string(decoded)
	}
	for _, p := range part.Parts {
		t, h := extractBodies(p)
		if text == "" {
			text = t
		}
		if html == "" {
			html = h
		}
	}
	return text, html
}

func buildRawMessage(msg provider.Message) string {
	var b strings.Builder
	b.WriteString("From: " + msg.From.String() + "\r\n")
	b.WriteString("To: " + formatAddresses(msg.To) + "\r\n")
	if len(msg.CC) > 0 {
		b.WriteString("Cc: " + formatAddresses(msg.CC) + "\r\n")
	}
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	if msg.InReplyTo != "" {
		b.WriteString("In-Reply-To: " + msg.InReplyTo + "\r\n")
	}
	if len(msg.References) > 0 {
		b.WriteString("References: " + strings.Join(msg.References, " ") + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")

	if msg.HTMLBody != "" {
		boundary := "boundary_mail_" + fmt.Sprintf("%d", time.Now().UnixNano())
		b.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")
		if msg.TextBody != "" {
			b.WriteString("--" + boundary + "\r\n")
			b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
			b.WriteString(msg.TextBody + "\r\n")
		}
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		b.WriteString(msg.HTMLBody + "\r\n")
		b.WriteString("--" + boundary + "--\r\n")
	} else {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(msg.TextBody + "\r\n")
	}

	return b.String()
}

func parseAddress(s string) provider.Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return provider.Address{}
	}
	// handle "Name <email>" format
	if idx := strings.LastIndex(s, "<"); idx >= 0 {
		name := strings.TrimSpace(s[:idx])
		name = strings.Trim(name, "\"")
		email := strings.TrimRight(s[idx+1:], ">")
		return provider.Address{Name: name, Email: strings.TrimSpace(email)}
	}
	return provider.Address{Email: s}
}

func parseAddresses(s string) []provider.Address {
	if s == "" {
		return nil
	}
	var addrs []provider.Address
	for _, part := range strings.Split(s, ",") {
		if a := parseAddress(part); a.Email != "" {
			addrs = append(addrs, a)
		}
	}
	return addrs
}

func formatAddresses(addrs []provider.Address) string {
	var parts []string
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func parseDate(s string) time.Time {
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2 Jan 2006 15:04:05 -0700",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func hasLabel(labels []string, target string) bool {
	for _, l := range labels {
		if l == target {
			return true
		}
	}
	return false
}

func isHiddenLabel(id string) bool {
	hidden := map[string]bool{
		"CHAT":      true,
		"CATEGORY_PERSONAL":    true,
		"CATEGORY_SOCIAL":      true,
		"CATEGORY_PROMOTIONS":  true,
		"CATEGORY_UPDATES":     true,
		"CATEGORY_FORUMS":      true,
	}
	return hidden[id]
}

func labelDisplayName(name string) string {
	replacer := strings.NewReplacer(
		"INBOX", "Inbox",
		"SENT", "Sent",
		"DRAFT", "Drafts",
		"TRASH", "Trash",
		"SPAM", "Spam",
		"STARRED", "Starred",
		"IMPORTANT", "Important",
		"UNREAD", "Unread",
	)
	return replacer.Replace(name)
}

func labelOrder(id string) int {
	order := map[string]int{
		"INBOX":     0,
		"STARRED":   1,
		"SENT":      2,
		"DRAFT":     3,
		"IMPORTANT": 4,
		"SPAM":      5,
		"TRASH":     6,
	}
	if o, ok := order[id]; ok {
		return o
	}
	return 100
}
