-- name: broadcast
-- description: Queue a message to every idle session, optionally filtered by project path and/or session tag
-- param: message! the prompt to queue on each matching session
-- param: project repo root path filter; omit to match every project
-- param: tag session tag filter; omit to match regardless of tags
-- actions: queue

local function has_tag(s, tag)
  for _, t in ipairs(s.tags or {}) do
    if t == tag then return true end
  end
  return false
end

local steps = {}
for _, s in ipairs(sessions()) do
  local match = (params.project == nil or params.project == "" or s.cwd == params.project)
  if match and params.tag ~= nil and params.tag ~= "" then
    match = has_tag(s, params.tag)
  end
  if match and s.status == "idle" then
    steps[#steps + 1] = { op = "queue", session_id = s.id, message = params.message }
  end
end
return steps
