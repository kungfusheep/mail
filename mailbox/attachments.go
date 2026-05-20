package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *State) OpenAttachment(row AttachmentRow) {
	name := row.Filename
	if name == "" {
		name = "attachment"
	}
	m.notifyInfo(fmt.Sprintf("opening %s...", name))

	if m.imap == nil {
		m.notifyError("attachment: not connected")
		return
	}
	if row.MessageID == "" || len(row.Part) == 0 {
		m.notifyError("attachment: file part unavailable")
		return
	}

	go func() {
		data, err := m.fetchAttachment(row)
		if err != nil {
			m.notifyError(fmt.Sprintf("attachment: %v", err))
			return
		}

		path, err := writeAttachmentTemp(name, data)
		if err != nil {
			m.notifyError(fmt.Sprintf("attachment: %v", err))
			return
		}

		open := m.attachmentOpener
		if open == nil {
			open = OpenSystemFile
		}
		if err := open(path); err != nil {
			m.notifyError(fmt.Sprintf("open attachment: %v", err))
			return
		}
	}()
}

func (m *State) fetchAttachment(row AttachmentRow) ([]byte, error) {
	if m.attachmentFetcher != nil {
		return m.attachmentFetcher(row)
	}
	if m.imap == nil {
		return nil, fmt.Errorf("not connected")
	}
	var lastErr error
	for _, folder := range m.fetchFolderCandidates(row.ThreadID) {
		if folder != "" {
			if err := m.imap.SelectFolder(folder); err != nil {
				lastErr = err
				continue
			}
		}
		data, err := m.imap.GetAttachment(row.MessageID, row.Part, row.ContentType, row.Encoding)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no folder candidates")
}

func writeAttachmentTemp(filename string, data []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "mail-attachments")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := safeAttachmentFilename(filename)
	f, err := os.CreateTemp(dir, "mail-*-"+name)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func safeAttachmentFilename(filename string) string {
	name := filepath.Base(filename)
	name = strings.TrimSpace(name)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "attachment"
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':':
			return '-'
		default:
			return r
		}
	}, name)
	return name
}
