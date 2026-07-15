package daemon

import (
	"log"
	"os/exec"
	"strings"
)

// Headless OS delivery (W9 §4). With no TUI subscriber attached, the live
// pushCopilotStream "attention" event has no recipient — so for the class that
// actually merits interruption (high-salience immediate notifies), the shared
// deliverNotify path also emits ONE desktop notification. Digest-class notifies
// are never OS-pushed; they coalesce into the durable digest and replay to the
// next attaching TUI. Quiet hours suppress the OS notification but never the
// durable item — the interruption is withheld, the record is not.

// deliverOSNotification posts one desktop notification, honoring the test seam.
func (d *Daemon) deliverOSNotification(title, body string) {
	if d.notifyOS != nil {
		d.notifyOS(title, body)
		return
	}
	osNotify(title, body)
}

// osNotify posts a best-effort macOS desktop notification: terminal-notifier if
// installed (richer), else osascript (always present on macOS). Failure is
// logged, never fatal — a missing notifier must not wedge the reactive path.
func osNotify(title, body string) {
	if path, err := exec.LookPath("terminal-notifier"); err == nil {
		if err := exec.Command(path, "-title", title, "-message", body).Run(); err != nil {
			log.Printf("reactive: terminal-notifier: %v", err)
		}
		return
	}
	script := "display notification " + osaQuote(body) + " with title " + osaQuote(title)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		log.Printf("reactive: osascript notify: %v", err)
	}
}

// osaQuote wraps s as an AppleScript string literal, escaping backslashes and
// double quotes so a message body cannot break out of the literal.
func osaQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
