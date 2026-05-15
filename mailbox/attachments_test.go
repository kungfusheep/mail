package mailbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAttachmentTempWritesSafeFile(t *testing.T) {
	path, err := writeAttachmentTemp("../invoice:may.pdf", []byte("%PDF-1.4"))
	if err != nil {
		t.Fatalf("writeAttachmentTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if !strings.Contains(filepath.Base(path), "invoice-may.pdf") {
		t.Fatalf("path = %q, want sanitised filename", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp attachment: %v", err)
	}
	if string(data) != "%PDF-1.4" {
		t.Fatalf("data = %q, want %%PDF-1.4", data)
	}
}
