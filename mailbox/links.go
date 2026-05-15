package mailbox

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
)

type LinkOpener func(string) error

func OpenSystemURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http", "https", "mailto":
	case "":
		return fmt.Errorf("missing URL scheme")
	default:
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	return exec.Command("open", raw).Start()
}

func OpenSystemFile(path string) error {
	if path == "" {
		return fmt.Errorf("missing file path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return exec.Command("open", abs).Start()
}
