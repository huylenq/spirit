package daemon

import (
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
)

// Permission handling. The wire client no longer decides anything: it hands each
// session/request_permission up to a daemon-provided handler (onPermission) that
// returns the option id to select, or "" to refuse the request. W4 makes that
// handler a real TUI round-trip (see acp_permission_flow.go). The typed parser
// (parsePermissionRequest) below extracts Hermes's real payload — the diff for an
// edit approval, the command for a dangerous execute, and the offered options — so
// the daemon can forward a faithful decision prompt to the human.
//
// The W0 auto-deny gate (decidePermission / isSensitivePath) is retained: the
// forwarding flow reuses isSensitivePath/sensitivePathHit to FLAG sensitive-path
// requests in the confirmation UI (they are surfaced, not auto-denied, now that a
// real approval flow exists), and decidePermission remains the documented fallback
// policy plus the anchor for the sensitive-path unit tests.

// serveAgentRequest answers an agent-to-client request. It runs in its own
// goroutine (spawned by the reader) so a permission prompt can be resolved while
// a session/prompt is still streaming.
func (c *acpClient) serveAgentRequest(msg acpMessage) {
	if msg.ID == nil {
		return
	}
	switch msg.Method {
	case "session/request_permission":
		optionID := c.resolvePermission(msg.Params)
		var outcome map[string]any
		if optionID != "" {
			outcome = map[string]any{"outcome": "selected", "optionId": optionID}
		} else {
			// No option to select — cancel the request, which refuses the tool
			// call. Fail closed for a denial we couldn't express as an option.
			outcome = map[string]any{"outcome": "cancelled"}
		}
		c.writeMessage(acpResponse{ //nolint:errcheck
			JSONRPC: "2.0",
			ID:      *msg.ID,
			Result:  map[string]any{"outcome": outcome},
		})
	default:
		log.Printf("acp: unknown agent request: %s", msg.Method)
		c.writeMessage(acpResponse{ //nolint:errcheck
			JSONRPC: "2.0",
			ID:      *msg.ID,
			Error:   &acpError{Code: -32601, Message: "not supported: " + msg.Method},
		})
	}
}

// resolvePermission delegates to the daemon-provided handler, or applies the W0
// policy inline when no handler is wired (e.g. in client-only tests).
func (c *acpClient) resolvePermission(params json.RawMessage) string {
	if c.onPermission != nil {
		return c.onPermission(params)
	}
	optionID, denied, reason := decidePermission(params)
	if denied {
		log.Printf("acp: auto-denied permission request (%s)", reason)
	}
	return optionID
}

// acpPermissionRequest is the session/request_permission payload: the tool call
// Hermes wants to run and the option kinds it offers as answers.
type acpPermissionRequest struct {
	ToolCall struct {
		Kind      string `json:"kind"` // "read", "edit", "execute", "delete", ...
		Locations []struct {
			Path string `json:"path"`
		} `json:"locations"`
		Content  json.RawMessage `json:"content"`
		RawInput json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []struct {
		OptionID string `json:"optionId"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// parsedPermission is the fully decoded session/request_permission payload the
// daemon forwards to the human. It flattens Hermes's ToolCallUpdate + options into
// exactly what the TUI needs to render an honest decision prompt.
type parsedPermission struct {
	ToolCallID   string
	Title        string
	Kind         string // "edit", "execute", ...
	Command      string // set for dangerous-command (execute) requests
	Diffs        []CopilotPermissionDiff
	Options      []CopilotPermissionOption
	Sensitive    bool
	SensitiveHit string
}

// acpPermissionFull decodes the wire shape Hermes actually sends. Edit approvals
// carry a diff content block (type "diff", oldText/newText); dangerous-command
// approvals carry a text content block ("$ cmd") plus rawInput {command,description}.
type acpPermissionFull struct {
	ToolCall struct {
		ToolCallID string `json:"toolCallId"`
		Title      string `json:"title"`
		Kind       string `json:"kind"`
		Locations  []struct {
			Path string `json:"path"`
		} `json:"locations"`
		Content []struct {
			Type string `json:"type"`
			// diff block
			Path    string `json:"path"`
			OldText string `json:"oldText"`
			NewText string `json:"newText"`
			// content block
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
		RawInput json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []struct {
		OptionID string `json:"optionId"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
	} `json:"options"`
}

// parsePermissionRequest decodes a session/request_permission params blob into the
// typed payload forwarded to the human, assigning a stable keyboard accelerator to
// each option. It fails on an unparseable blob so the caller can fail closed.
func parsePermissionRequest(params json.RawMessage) (parsedPermission, error) {
	var p acpPermissionFull
	if err := json.Unmarshal(params, &p); err != nil {
		return parsedPermission{}, err
	}
	out := parsedPermission{
		ToolCallID: p.ToolCall.ToolCallID,
		Title:      p.ToolCall.Title,
		Kind:       p.ToolCall.Kind,
	}
	for _, block := range p.ToolCall.Content {
		switch block.Type {
		case "diff":
			out.Diffs = append(out.Diffs, CopilotPermissionDiff{
				Path:    block.Path,
				OldText: block.OldText,
				NewText: block.NewText,
			})
		case "content":
			if block.Content.Text != "" && out.Command == "" {
				out.Command = strings.TrimPrefix(strings.TrimSpace(block.Content.Text), "$ ")
			}
		}
	}
	// A dangerous-command request carries the command in rawInput too; prefer that.
	if len(p.ToolCall.RawInput) > 0 {
		var ri struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(p.ToolCall.RawInput, &ri) == nil && ri.Command != "" {
			out.Command = ri.Command
		}
	}
	// Reuse the W0 sensitive-path walker to FLAG (not deny) sensitive targets.
	var legacy acpPermissionRequest
	if json.Unmarshal(params, &legacy) == nil {
		if hit := sensitivePathHit(legacy); hit != "" {
			out.Sensitive = true
			out.SensitiveHit = hit
		}
	}
	out.Options = assignPermissionKeys(p.Options)
	return out, nil
}

// assignPermissionKeys maps Hermes's option ids/kinds to stable keyboard
// accelerators for the confirmation UI. Keying is by option id first (the stable
// discriminator — allow_session ships with kind allow_always), falling back to the
// kind hint, so the same key always means the same thing across the edit-approval
// (allow_once/deny) and dangerous-command (5-option) variants.
func assignPermissionKeys(opts []struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}) []CopilotPermissionOption {
	out := make([]CopilotPermissionOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, CopilotPermissionOption{
			OptionID: o.OptionID,
			Kind:     o.Kind,
			Name:     o.Name,
			Key:      permissionKeyFor(o.OptionID, o.Kind),
		})
	}
	return out
}

// permissionKeyFor returns the keyboard accelerator for an option.
//
//	y → allow_once      a → allow_always / allow_session (persistent allow)
//	n → reject_once     N → reject_always / deny_always  (persistent deny)
func permissionKeyFor(optionID, kind string) string {
	switch optionID {
	case "allow_once":
		return "y"
	case "allow_session", "allow_always":
		return "a"
	case "deny", "reject_once":
		return "n"
	case "deny_always", "reject_always":
		return "N"
	}
	switch strings.ToLower(kind) {
	case "allow_once":
		return "y"
	case "allow_always":
		return "a"
	case "reject_once":
		return "n"
	case "reject_always":
		return "N"
	}
	return ""
}

// isAllowOptionID reports whether the chosen option id/kind is an allow (vs a
// reject). Used to classify the outcome for the receipt line in the transcript.
func (p parsedPermission) isAllowOptionID(optionID string) bool {
	for _, o := range p.Options {
		if o.OptionID == optionID {
			k := strings.ToLower(o.Kind)
			return strings.HasPrefix(k, "allow")
		}
	}
	return false
}

// decidePermission is the W0 auto-approval gate. It denies any edit-kind request
// and any request touching a sensitive path (returning the deny option id and a
// human-readable reason); otherwise it keeps the legacy broadest-allow behavior.
func decidePermission(params json.RawMessage) (optionID string, denied bool, reason string) {
	var p acpPermissionRequest
	if err := json.Unmarshal(params, &p); err != nil {
		// Can't understand the request — fail closed.
		return "", true, "unparseable permission request"
	}

	if strings.EqualFold(p.ToolCall.Kind, "edit") {
		return selectRejectOption(p), true, "edit-kind tool call"
	}
	if hit := sensitivePathHit(p); hit != "" {
		return selectRejectOption(p), true, "sensitive path: " + hit
	}
	return selectAllowOption(params), false, ""
}

// sensitivePathHit returns the first sensitive path referenced anywhere in the
// tool call (declared edit locations, diff content, or raw command arguments),
// or "" if none is found.
func sensitivePathHit(p acpPermissionRequest) string {
	for _, loc := range p.ToolCall.Locations {
		if isSensitivePath(loc.Path) {
			return loc.Path
		}
	}
	var found []string
	collectSensitivePaths(p.ToolCall.Content, &found)
	collectSensitivePaths(p.ToolCall.RawInput, &found)
	if len(found) > 0 {
		return found[0]
	}
	return ""
}

// collectSensitivePaths walks an arbitrary JSON blob and appends any string value
// that looks like a sensitive path — catching diff-block paths and command
// arguments alike without assuming Hermes's exact payload shape.
func collectSensitivePaths(raw json.RawMessage, found *[]string) {
	if len(raw) == 0 {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return
	}
	walkSensitive(v, found)
}

func walkSensitive(v any, found *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for _, val := range t {
			walkSensitive(val, found)
		}
	case []any:
		for _, val := range t {
			walkSensitive(val, found)
		}
	case string:
		if isSensitivePath(t) {
			*found = append(*found, t)
		}
	}
}

// isSensitivePath reports whether a path (or a command string that embeds one)
// touches a location Hermes deliberately always prompts for: VCS/credential dirs,
// dotenv files, and private keys.
func isSensitivePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	slashed := filepath.ToSlash(s)
	for _, seg := range strings.Split(slashed, "/") {
		seg = strings.ToLower(strings.TrimSpace(seg))
		switch seg {
		case ".git", ".ssh", ".gnupg", ".aws", ".kube":
			return true
		}
		if strings.HasPrefix(seg, ".env") {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(slashed))
	switch {
	case strings.HasPrefix(base, ".env"):
		return true
	case strings.HasPrefix(base, "id_rsa"),
		strings.HasPrefix(base, "id_ed25519"),
		strings.HasPrefix(base, "id_ecdsa"),
		strings.HasPrefix(base, "id_dsa"):
		return true
	case strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".key"):
		return true
	case base == ".npmrc", base == ".netrc", base == "credentials":
		return true
	}
	return false
}

// selectAllowOption parses a session/request_permission params blob and returns the
// id of the option with the broadest allow-kind (allow_always > allow_session >
// allow_once). Returns "" if no allow option is offered.
func selectAllowOption(params json.RawMessage) string {
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	rank := map[string]int{"allow_always": 3, "allow_session": 2, "allow_once": 1}
	best, bestRank := "", 0
	for _, opt := range p.Options {
		if r := rank[opt.Kind]; r > bestRank {
			best, bestRank = opt.OptionID, r
		}
	}
	return best
}

// selectRejectOption returns the id of a deny/reject option Hermes offered, or ""
// if none was offered (the caller then cancels the request to refuse it).
func selectRejectOption(p acpPermissionRequest) string {
	for _, opt := range p.Options {
		k := strings.ToLower(opt.Kind)
		if k == "deny" || strings.HasPrefix(k, "reject") {
			return opt.OptionID
		}
	}
	return ""
}
