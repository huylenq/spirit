package claude

// FileOverlap records a file being edited by multiple sessions.
type FileOverlap struct {
	FilePath   string   `json:"filePath"`
	PaneIDs    []string `json:"paneIDs"`
	SessionIDs []string `json:"sessionIDs"`
}

// DetectOverlaps finds files edited by 2+ sessions.
func DetectOverlaps(sessions []ClaudeSession) []FileOverlap {
	type entry struct {
		paneID    string
		sessionID string
	}
	fileMap := make(map[string][]entry)

	for _, s := range sessions {
		if s.SessionID == "" || s.IsPhantom {
			continue
		}
		stats := ReadDiffStats(s.SessionID)
		for filePath := range stats {
			fileMap[filePath] = append(fileMap[filePath], entry{s.PaneID, s.SessionID})
		}
	}

	var overlaps []FileOverlap
	for filePath, entries := range fileMap {
		if len(entries) < 2 {
			continue
		}
		o := FileOverlap{FilePath: filePath}
		for _, e := range entries {
			o.PaneIDs = append(o.PaneIDs, e.paneID)
			o.SessionIDs = append(o.SessionIDs, e.sessionID)
		}
		overlaps = append(overlaps, o)
	}
	return overlaps
}
