// Package consent implements the interactive policy used by the PDF browser
// fallback to decide whether it may download a browser when none is
// installed.
package consent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Policy builds the download-consent function: with allowDownload it always
// consents; otherwise it prompts on an interactive terminal and denies when
// there is no terminal to prompt on.
func Policy(allowDownload bool) func() (bool, error) {
	return func() (bool, error) {
		if allowDownload {
			return true, nil
		}
		if !stdinIsTerminal() {
			return false, nil
		}
		fmt.Fprint(os.Stderr, "No Chrome/Chromium found. Download Chromium (~150MB) to render the PDF? [y/N]: ")
		return Read(os.Stdin)
	}
}

// Read reads a single line and reports whether it affirms (y/yes,
// case-insensitive). Anything else, or a read error, is treated as a decline.
func Read(r io.Reader) (bool, error) {
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// stdinIsTerminal reports whether standard input is an interactive terminal.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
