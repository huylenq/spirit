package scripting

import (
	"github.com/huylenq/claude-mission-control/internal/daemon"
	lua "github.com/yuin/gopher-lua"
)

// registerBacklogAPIs registers backlog_list(), backlog_create(), backlog_update(), backlog_delete() into the VM.
func registerBacklogAPIs(L *lua.LState, client *daemon.Client) {
	L.SetGlobal("backlog_list", L.NewFunction(luaBacklogList(client)))
	L.SetGlobal("backlog_create", L.NewFunction(luaBacklogCreate(client)))
	L.SetGlobal("backlog_update", L.NewFunction(luaBacklogUpdate(client)))
	L.SetGlobal("backlog_delete", L.NewFunction(luaBacklogDelete(client)))
}

// backlog_list(cwd) → array of backlog tables
func luaBacklogList(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		items, err := client.BacklogList(cwd)
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

// backlog_create(cwd, body) → backlog table
func luaBacklogCreate(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		body := L.CheckString(2)
		item, err := client.BacklogCreate(cwd, body)
		if err != nil {
			L.RaiseError("backlog_create: %v", err)
			return 0
		}
		L.Push(backlogToTable(L, item))
		return 1
	}
}

// backlog_update(cwd, id, body) → backlog table
func luaBacklogUpdate(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		id := L.CheckString(2)
		body := L.CheckString(3)
		item, err := client.BacklogUpdate(cwd, id, body)
		if err != nil {
			L.RaiseError("backlog_update: %v", err)
			return 0
		}
		L.Push(backlogToTable(L, item))
		return 1
	}
}

// backlog_delete(cwd, id)
func luaBacklogDelete(client *daemon.Client) lua.LGFunction {
	return func(L *lua.LState) int {
		cwd := L.CheckString(1)
		id := L.CheckString(2)
		if err := client.BacklogDelete(cwd, id); err != nil {
			L.RaiseError("backlog_delete: %v", err)
			return 0
		}
		return 0
	}
}

