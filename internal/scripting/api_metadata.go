package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// set_tags(id, tags) -> {ok, operation, target}
// Category: Features
// Replace a session's tags with the given array (persisted and broadcast to
// subscribers). Passing an empty table clears the tags.
func luaSetTags(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		tbl := L.CheckTable(2)

		tags := make([]string, 0, tbl.Len())
		tbl.ForEach(func(_, v lua.LValue) {
			tags = append(tags, v.String())
		})

		if err := deps.Client.SetTags(id, tags); err != nil {
			L.RaiseError("set_tags: %v", err)
			return 0
		}
		L.Push(mutationResult(L, "set_tags", id))
		return 1
	}
}

// set_note(id, note) -> {ok, operation, target}
// Category: Features
// Set a session's note (persisted and broadcast to subscribers). An empty string
// clears it.
func luaSetNote(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		id := L.CheckString(1)
		note := L.CheckString(2)

		if err := deps.Client.SetNote(id, note); err != nil {
			L.RaiseError("set_note: %v", err)
			return 0
		}
		L.Push(mutationResult(L, "set_note", id))
		return 1
	}
}
