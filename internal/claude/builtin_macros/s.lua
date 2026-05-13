-- key: s
-- name: simplify
local id = selected()
if not id or id == "" then
  flash("no session selected")
  return
end
local s = session(id)
if not s then
  flash("session not found")
  return
end
if s.status ~= "idle" then
  flash("session is busy")
  return
end
auto_jump(id)
flash("simplify started")
send(id, "/simplify")
