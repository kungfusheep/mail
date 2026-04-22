package imap

import (
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"time"

	imaplib "github.com/emersion/go-imap/v2"

	"github.com/kungfusheep/mail/provider"
)

// SaveDraft appends a draft message to the given folder (typically
// "[Gmail]/Drafts") and returns the new UID assigned by the server. If
// prevUID is non-empty, the previous draft at that UID is expunged after
// the new one lands — so the server-side Drafts folder doesn't accumulate
// stale copies as the user edits.
//
// The primary connection's active mailbox is unspecified on return; callers
// that rely on SELECT state must re-select.
func (im *IMAP) SaveDraft(folder string, msg provider.Message, prevUID string) (string, error) {
	return withRetry(im, func() (string, error) {
		raw := buildRFC822(im.config.Email, msg)
		opts := &imaplib.AppendOptions{
			Flags: []imaplib.Flag{imaplib.FlagDraft, imaplib.FlagSeen},
			Time:  time.Now(),
		}
		cmd := im.client.Append(folder, int64(len(raw)), opts)
		if _, err := cmd.Write([]byte(raw)); err != nil {
			return "", fmt.Errorf("append write: %w", err)
		}
		if err := cmd.Close(); err != nil {
			return "", fmt.Errorf("append close: %w", err)
		}
		data, err := cmd.Wait()
		if err != nil {
			return "", fmt.Errorf("append: %w", err)
		}
		newUID := fmt.Sprintf("%d", data.UID)

		if prevUID != "" && prevUID != newUID {
			if _, err := im.client.Select(folder, nil).Wait(); err != nil {
				// new draft is saved — we couldn't delete the old, but
				// surface the error so the caller can see duplicates forming.
				return newUID, fmt.Errorf("select %s for expunge: %w", folder, err)
			}
			var uid imaplib.UID
			fmt.Sscanf(prevUID, "%d", &uid)
			uidSet := imaplib.UIDSetNum(uid)
			storeFlags := imaplib.StoreFlags{
				Op:    imaplib.StoreFlagsAdd,
				Flags: []imaplib.Flag{imaplib.FlagDeleted},
			}
			if err := im.client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
				log.Printf("imap.SaveDraft: store \\Deleted on UID %s failed: %v", prevUID, err)
				return newUID, fmt.Errorf("store deleted: %w", err)
			}
			if err := im.client.Expunge().Close(); err != nil {
				log.Printf("imap.SaveDraft: expunge after store failed: %v", err)
				return newUID, fmt.Errorf("expunge: %w", err)
			}
			log.Printf("imap.SaveDraft: expunged prev UID=%s after new append UID=%s", prevUID, newUID)
		}
		return newUID, nil
	})
}

// DeleteDraft marks the given UID in the Drafts folder as \Deleted and
// expunges it. Used when the user discards a draft entirely.
func (im *IMAP) DeleteDraft(folder string, uid string) error {
	_, err := withRetry(im, func() (struct{}, error) {
		if _, err := im.client.Select(folder, nil).Wait(); err != nil {
			return struct{}{}, fmt.Errorf("select %s: %w", folder, err)
		}
		var u imaplib.UID
		fmt.Sscanf(uid, "%d", &u)
		uidSet := imaplib.UIDSetNum(u)
		storeFlags := imaplib.StoreFlags{
			Op:    imaplib.StoreFlagsAdd,
			Flags: []imaplib.Flag{imaplib.FlagDeleted},
		}
		if err := im.client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
			return struct{}{}, fmt.Errorf("store: %w", err)
		}
		if err := im.client.Expunge().Close(); err != nil {
			return struct{}{}, fmt.Errorf("expunge: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

// buildRFC822 serialises a provider.Message into an RFC 822 message suitable
// for APPEND. Mirrors the SMTP send path so drafts and sent messages use
// identical headers and MIME structure.
func buildRFC822(fromEmail string, msg provider.Message) string {
	var b strings.Builder
	b.WriteString("From: " + fromEmail + "\r\n")
	b.WriteString("To: " + formatAddresses(msg.To) + "\r\n")
	if len(msg.CC) > 0 {
		b.WriteString("Cc: " + formatAddresses(msg.CC) + "\r\n")
	}
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	if msg.MessageID == "" {
		msg.MessageID = newMessageID(fromEmail)
	}
	b.WriteString("Message-ID: <" + msg.MessageID + ">\r\n")
	if msg.InReplyTo != "" {
		b.WriteString("In-Reply-To: " + msg.InReplyTo + "\r\n")
	}
	if len(msg.References) > 0 {
		b.WriteString("References: " + strings.Join(msg.References, " ") + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")

	if msg.HTMLBody != "" {
		boundary := fmt.Sprintf("boundary_%d", time.Now().UnixNano())
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

func formatAddresses(addrs []provider.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func newMessageID(email string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := email
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain = email[at+1:]
	}
	return fmt.Sprintf("%x.%x@%s", b[:8], b[8:], domain)
}
