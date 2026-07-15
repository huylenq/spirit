-- name: broadcast
-- description: Queue a message to every idle session, optionally filtered by project path
-- param: message! the prompt to queue on each matching session
-- param: project repo root path filter; omit to match every project
-- actions: queue

local steps = {}
for _, s in ipairs(sessions()) do
  local match = (params.project == nil or params.project == "" or s.cwd == params.project)
  if match and s.status == "idle" then
    steps[#steps + 1] = { op = "queue", session_id = s.id, message = params.message }
  end
end
return steps
