package smtp

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kungfusheep/mail/provider"
)

func TestBuildMessageIncludesLocalAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invoice.txt")
	if err := os.WriteFile(path, []byte("attachment body"), 0600); err != nil {
		t.Fatal(err)
	}

	raw, err := buildMessage("me@example.test", provider.Message{
		To:       []provider.Address{{Email: "you@example.test"}},
		Subject:  "hello",
		TextBody: "body",
		Attachments: []provider.Attachment{{
			Filename:    "invoice.txt",
			ContentType: "text/plain",
			LocalPath:   path,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Content-Type: multipart/mixed;",
		"Content-Disposition: attachment; filename=\"invoice.txt\"",
		"Content-Transfer-Encoding: base64",
		base64.StdEncoding.EncodeToString([]byte("attachment body")),
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("message missing %q:\n%s", want, raw)
		}
	}
}

func TestBuildMessageReturnsAttachmentReadError(t *testing.T) {
	_, err := buildMessage("me@example.test", provider.Message{
		To:       []provider.Address{{Email: "you@example.test"}},
		Subject:  "hello",
		TextBody: "body",
		Attachments: []provider.Attachment{{
			Filename:  "missing.txt",
			LocalPath: filepath.Join(t.TempDir(), "missing.txt"),
		}},
	})
	if err == nil {
		t.Fatal("buildMessage err = nil, want missing attachment error")
	}
}
