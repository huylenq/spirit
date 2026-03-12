package daemon

import (
	"encoding/json"
	"sort"

	"github.com/huylenq/claude-mission-control/internal/claude"
)

func (d *Daemon) handleHookEvents(data json.RawMessage) *Response {
	var req SessionIDData
	if err := json.Unmarshal(data, &req); err != nil {
		r := errResponse("bad data: " + err.Error())
		return &r
	}
	events, _ := claude.ReadHookEvents(req.SessionID)
	r := resultResponse(HookEventsData{Events: events})
	return &r
}

func (d *Daemon) handleAllHookEffects() *Response {
	d.mu.RLock()
	sessions := d.sessions
	d.mu.RUnlock()

	var all []claude.GlobalHookEffect
	for _, s := range sessions {
		if s.SessionID == "" {
			continue
		}
		events, _ := claude.ReadHookEvents(s.SessionID)
		for _, ev := range events {
			if ev.Effect == "" || ev.Effect == claude.HookEffectNone {
				continue
			}
			all = append(all, claude.GlobalHookEffect{
				Time:      ev.Time,
				HookType:  ev.HookType,
				Effect:    ev.Effect,
				AnimalIdx: s.AvatarAnimalIdx,
				ColorIdx:  s.AvatarColorIdx,
			})
		}
	}
	// Sort by time descending (newest first). HH:MM:SS is lexicographically sortable.
	sort.Slice(all, func(i, j int) bool { return all[i].Time > all[j].Time })

	// Merge consecutive identical entries (same hook type, effect, and avatar)
	var merged []claude.GlobalHookEffect
	for _, e := range all {
		e.Count = 1
		if n := len(merged); n > 0 {
			prev := &merged[n-1]
			if prev.HookType == e.HookType && prev.Effect == e.Effect &&
				prev.AnimalIdx == e.AnimalIdx && prev.ColorIdx == e.ColorIdx {
				prev.Count++
				continue
			}
		}
		merged = append(merged, e)
	}
	if len(merged) > 25 {
		merged = merged[:25]
	}
	r := resultResponse(AllHookEffectsData{Effects: merged})
	return &r
}
