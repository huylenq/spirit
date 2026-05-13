-- key: d
-- name: simplify+commit+done
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
if s.commit_done_pending then
  flash("simplify+commit+done already pending")
  return
end
auto_jump(id)
flash("simplify+commit+done started")
send(id, "/simplify", { wait = "cycle" })
commit_done(id)
