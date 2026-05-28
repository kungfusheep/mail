package mailbox

import (
	"os/exec"
	"strings"
)

var copyText = copyTextToPasteboard

func copyTextToPasteboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
