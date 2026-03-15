package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/huylenq/claude-mission-control/internal/claude"
	"github.com/huylenq/claude-mission-control/internal/ui"
)

func sidebarStatePath() string {
	return filepath.Join(claude.StatusDir(), "sidebar_state.json")
}

func loadSidebarState() ui.SidebarState {
	data, err := os.ReadFile(sidebarStatePath())
	if err != nil {
		return ui.SidebarState{}
	}
	var st ui.SidebarState
	if json.Unmarshal(data, &st) != nil {
		return ui.SidebarState{}
	}
	return st
}

func saveSidebarState(st ui.SidebarState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(sidebarStatePath(), data, 0644)
}
