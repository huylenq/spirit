package scripting

import (
	lua "github.com/yuin/gopher-lua"
)

// backlog_list(cwd) -> []backlog
// Category: Backlog
// List all backlog items for the given working directory.
func luaBacklogList(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		items, err := deps.Client.BacklogList(cwd)
		if err != nil {
			L.RaiseError("backlog_list: %v", err)
			return 0
		}
		tbl := L.NewTable()
		for i, item := range items {
			tbl.RawSetInt(i+1, backlogToTable(L, item))
		}
		L.Push(tbl)
		return 1
	}
}

// backlog_create(cwd, body) -> backlog
// Category: Backlog
// Create a new backlog item.
func luaBacklogCreate(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		body := L.CheckString(2)
		item, err := deps.Client.BacklogCreate(cwd, body)
		if err != nil {
			L.RaiseError("backlog_create: %v", err)
			return 0
		}
		L.Push(backlogToTable(L, item))
		return 1
	}
}

// backlog_update(cwd, id, body) -> backlog
// Category: Backlog
// Update an existing backlog item's body.
func luaBacklogUpdate(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		id := L.CheckString(2)
		body := L.CheckString(3)
		item, err := deps.Client.BacklogUpdate(cwd, id, body)
		if err != nil {
			L.RaiseError("backlog_update: %v", err)
			return 0
		}
		L.Push(backlogToTable(L, item))
		return 1
	}
}

// backlog_delete(cwd, id)
// Category: Backlog
// Delete a backlog item.
func luaBacklogDelete(deps Deps) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		id := L.CheckString(2)
		if err := deps.Client.BacklogDelete(cwd, id); err != nil {
			L.RaiseError("backlog_delete: %v", err)
			return 0
		}
		return 0
	}
}
