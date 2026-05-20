package provider

import (
	"strings"
	"time"
)

type Address struct {
	Name  string
	Email string
}

func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return a.Name + " <" + a.Email + ">"
}

func ParseAddressList(s string) []Address {
	if s == "" {
		return nil
	}
	var addrs []Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.LastIndex(part, "<"); idx >= 0 {
			name := strings.TrimSpace(part[:idx])
			email := strings.TrimSpace(strings.TrimRight(part[idx+1:], ">"))
			addrs = append(addrs, Address{Name: name, Email: email})
		} else {
			addrs = append(addrs, Address{Email: part})
		}
	}
	return addrs
}

type Folder struct {
	ID   string
	Name string
	// unread message count
	Unread int
	// total message count
	Total int
}

type Message struct {
	ID string
	// thread this message belongs to
	ThreadID string
	From     Address
	To       []Address
	CC       []Address
	BCC      []Address
	Subject  string
	Date     time.Time
	// plain text body
	TextBody string
	// html body
	HTMLBody string
	// attachment metadata; file content is fetched separately when supported
	Attachments []Attachment
	Read        bool
	Starred     bool
	Labels      []string
	// headers needed for reply threading
	MessageID  string
	InReplyTo  string
	References []string
}

type Attachment struct {
	Filename    string
	ContentType string
	Size        int64
	Part        []int
	Encoding    string
}

func AttachmentIcon(filename, contentType string) string {
	name := strings.ToLower(filename)
	contentType = strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(contentType, "image/") ||
		strings.HasSuffix(name, ".png") ||
		strings.HasSuffix(name, ".jpg") ||
		strings.HasSuffix(name, ".jpeg") ||
		strings.HasSuffix(name, ".gif") ||
		strings.HasSuffix(name, ".webp"):
		return "󰋩"
	case strings.HasPrefix(contentType, "audio/") ||
		strings.HasSuffix(name, ".mp3") ||
		strings.HasSuffix(name, ".wav") ||
		strings.HasSuffix(name, ".m4a"):
		return "󰎆"
	case strings.HasPrefix(contentType, "video/") ||
		strings.HasSuffix(name, ".mov") ||
		strings.HasSuffix(name, ".mp4") ||
		strings.HasSuffix(name, ".mkv"):
		return "󰈫"
	case strings.Contains(contentType, "calendar") || strings.HasSuffix(name, ".ics"):
		return "󰃭"
	case strings.Contains(contentType, "pdf") || strings.HasSuffix(name, ".pdf"):
		return "󰈦"
	case strings.HasSuffix(name, ".zip") ||
		strings.HasSuffix(name, ".tar") ||
		strings.HasSuffix(name, ".gz"):
		return "󰛫"
	default:
		return "󰈔"
	}
}

type Thread struct {
	ID      string
	Subject string
	// most recent message snippet
	Snippet  string
	Messages []Message
	Unread   int
	Date     time.Time
	// participants across the thread
	Participants []Address
}

type ListOptions struct {
	Folder     string
	Query      string
	PageToken  string
	MaxResults int
}

type ListResult struct {
	Threads       []Thread
	NextPageToken string
}

type Provider interface {
	// auth
	Authenticate() error

	// folders
	ListFolders() ([]Folder, error)

	// threads
	ListThreads(opts ListOptions) (ListResult, error)
	GetThread(id string) (Thread, error)

	// messages
	GetMessage(id string) (Message, error)

	// actions
	Send(msg Message) error
	Reply(threadID string, msg Message) error
	// ApplyLabels adjusts label membership for the given messages. Providers
	// with single-label semantics (IMAP) treat (add, remove) pairs as a MOVE;
	// providers with multi-label semantics (Gmail API) apply each side
	// independently. Callers pass one of:
	//   add=[X], remove=[Y]  → move from Y to X
	//   add=[X]              → tag with X (no removal)
	//   remove=[Y]           → archive out of Y
	ApplyLabels(messageIDs []string, add []string, remove []string) error
	Archive(messageIDs []string) error
	Delete(messageIDs []string) error
	MarkRead(messageIDs []string, read bool) error
	Star(messageIDs []string, starred bool) error

	// search
	Search(query string, maxResults int) ([]Thread, error)
}
