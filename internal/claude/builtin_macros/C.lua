-- key: c
-- name: commit+done
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
  flash("commit+done already pending")
  return
end
commit_done(id)
auto_jump(id)
flash("commit+done started")
