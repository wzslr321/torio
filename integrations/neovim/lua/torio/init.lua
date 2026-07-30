local M = {}

local defaults = {
  torio_cmd = { "torio" },
  curl_cmd = { "curl" },
  api_url = "http://127.0.0.1:19119",
  width = 48,
  session_token = function()
    return vim.env.HERMES_SESSION_TOKEN
  end,
}

local config = vim.deepcopy(defaults)
local state = {
  buffer = nil,
  window = nil,
  projects = {},
  project_by_line = {},
  session_by_line = {},
  current_project = nil,
  view = "projects",
}

local function notify(message, level)
  vim.notify("torio.nvim: " .. message, level or vim.log.levels.INFO)
end

local function one_line(value)
  return tostring(value or ""):gsub("[\r\n\t]", " "):sub(1, 200)
end

local function command(base, args)
  local result = vim.deepcopy(base)
  vim.list_extend(result, args)
  return result
end

local function run_json(args, callback)
  vim.system(command(config.torio_cmd, args), { text = true }, function(result)
    vim.schedule(function()
      if result.code ~= 0 then
        local detail = vim.trim(result.stderr or "")
        notify(detail ~= "" and detail or ("torio exited " .. result.code), vim.log.levels.ERROR)
        callback(nil)
        return
      end
      local ok, envelope = pcall(vim.json.decode, result.stdout or "")
      if not ok or type(envelope) ~= "table" or envelope.ok ~= true then
        notify("invalid JSON envelope from torio", vim.log.levels.ERROR)
        callback(nil)
        return
      end
      callback(envelope.data or {})
    end)
  end)
end

local function set_lines(lines)
  if not state.buffer or not vim.api.nvim_buf_is_valid(state.buffer) then
    return
  end
  vim.bo[state.buffer].modifiable = true
  vim.api.nvim_buf_set_lines(state.buffer, 0, -1, false, lines)
  vim.bo[state.buffer].modifiable = false
end

local function selected_project()
  if not state.window or not vim.api.nvim_win_is_valid(state.window) then
    return nil
  end
  local project = state.project_by_line[vim.api.nvim_win_get_cursor(state.window)[1]]
  if project then
    return project
  end
  if state.view ~= "projects" then
    return state.current_project
  end
  return nil
end

local function open_terminal(project, push_capable)
  if not project then
    notify("select a project first", vim.log.levels.WARN)
    return
  end
  vim.cmd("botright 12split")
  local action = push_capable and "shell" or "enter"
  if push_capable then
    notify("opening push-capable operator shell; capability ends when this terminal exits", vim.log.levels.WARN)
  end
  vim.fn.termopen(command(config.torio_cmd, { "project", action, project.id }))
  vim.cmd("startinsert")
end

local function render_projects()
	state.view = "projects"
	state.current_project = nil
  state.project_by_line = {}
  state.session_by_line = {}
  local lines = {
    "Torio",
    "====",
    "<CR>/s sessions  t terminal  P push shell",
    "u use  h health  r refresh  q close",
    "",
    "Projects",
    "--------",
  }
  for _, project in ipairs(state.projects) do
    table.insert(lines, string.format("  %-20s %s", project.id, one_line(project.display_name)))
    state.project_by_line[#lines] = project
  end
  if #state.projects == 0 then
    table.insert(lines, "  no projects registered")
  end
  set_lines(lines)
end

local function refresh_projects()
  set_lines({ "Torio", "====", "loading projects..." })
  run_json({ "project", "list", "--json" }, function(data)
    if not data then
      return
    end
    state.projects = data.projects or {}
    render_projects()
  end)
end

local function use_project(project)
  if not project then
    notify("select a project first", vim.log.levels.WARN)
    return
  end
  run_json({ "project", "use", project.id, "--json" }, function(data)
    if data then
      notify("active Hermes project: " .. project.id)
      refresh_projects()
    end
  end)
end

local function render_health(project, data)
	state.view = "health"
	state.current_project = project
  state.project_by_line = {}
  state.session_by_line = {}
  local lines = {
    "Torio / " .. project.id,
    "====",
    "b projects  t terminal  P push shell  r refresh",
    "",
    "Project health",
    "--------------",
  }
  for _, key in ipairs({ "id", "display_name", "path", "remote", "next_step" }) do
    if data[key] ~= nil then
      table.insert(lines, string.format("%-16s %s", key .. ":", one_line(data[key])))
    end
  end
  local checkout = data.checkout or {}
  local hermes = data.hermes or {}
  table.insert(lines, "")
  table.insert(lines, "checkout:")
  for _, key in ipairs({ "path_exists", "repository", "origin_matches", "clean", "shared_permissions" }) do
    table.insert(lines, string.format("  %-20s %s", key, one_line(checkout[key])))
  end
  table.insert(lines, "hermes:")
  for _, key in ipairs({ "registered", "archived", "primary_matches" }) do
    table.insert(lines, string.format("  %-20s %s", key, one_line(hermes[key])))
  end
  local issues = data.issues or {}
  if #issues > 0 then
    table.insert(lines, "issues: " .. one_line(table.concat(issues, ", ")))
  end
  set_lines(lines)
end

local function show_health(project)
  project = project or selected_project()
  if not project then
    notify("select a project first", vim.log.levels.WARN)
    return
  end
  set_lines({ "Torio / " .. project.id, "====", "loading health..." })
  run_json({ "project", "show", project.id, "--json" }, function(data)
    if data then
      render_health(project, data)
    end
  end)
end

local function curl_config(token)
  if type(token) ~= "string" or token == "" then
    return nil, "configure session_token or HERMES_SESSION_TOKEN"
  end
  if token:find("[\r\n]") then
    return nil, "session token contains a newline"
  end
  token = token:gsub("\\", "\\\\"):gsub('"', '\\"')
  return 'header = "X-Hermes-Session-Token: ' .. token .. '"\n'
end

local function fetch_sessions(project, callback)
  local provider = config.session_token
  local token = type(provider) == "function" and provider() or nil
  local stdin, err = curl_config(token)
  if not stdin then
    notify(err, vim.log.levels.WARN)
    callback(nil)
    return
  end
  local args = command(config.curl_cmd, {
    "--config", "-",
    "--fail", "--silent", "--show-error",
    "--get", config.api_url .. "/api/sessions",
    "--data-urlencode", "cwd_prefix=" .. project.path,
    "--data-urlencode", "limit=50",
  })
  vim.system(args, { text = true, stdin = stdin }, function(result)
    vim.schedule(function()
      if result.code ~= 0 then
        notify("Hermes sessions request failed (exit " .. result.code .. ")", vim.log.levels.ERROR)
        callback(nil)
        return
      end
      local ok, data = pcall(vim.json.decode, result.stdout or "")
      if not ok or type(data) ~= "table" then
        notify("invalid JSON from Hermes sessions API", vim.log.levels.ERROR)
        callback(nil)
        return
      end
      callback(data.sessions or data)
    end)
  end)
end

local function show_sessions(project)
  project = project or selected_project()
  if not project then
    notify("select a project first", vim.log.levels.WARN)
    return
  end
  set_lines({ "Torio / " .. project.id, "====", "loading sessions..." })
  fetch_sessions(project, function(sessions)
    if not sessions then
      return
    end
    state.view = "sessions"
    state.current_project = project
    state.project_by_line = {}
    state.session_by_line = {}
    local lines = {
      "Torio / " .. project.id,
      "====",
      "b projects  t terminal  P push shell  r refresh  y copy id",
      "",
      "Hermes sessions",
      "---------------",
    }
    for _, session in ipairs(sessions) do
      local active = session.is_active and "●" or "○"
      local title = one_line(session.title or session.name or "untitled")
      table.insert(lines, string.format("%s %-36s %s", active, title, one_line(session.updated_at)))
      state.session_by_line[#lines] = session
    end
    if #sessions == 0 then
      table.insert(lines, "  no sessions for this workspace")
    end
    set_lines(lines)
  end)
end

local function ensure_panel()
  if state.window and vim.api.nvim_win_is_valid(state.window) then
    vim.api.nvim_set_current_win(state.window)
    return
  end
  vim.cmd("botright " .. config.width .. "vnew")
  state.window = vim.api.nvim_get_current_win()
  if not state.buffer or not vim.api.nvim_buf_is_valid(state.buffer) then
    state.buffer = vim.api.nvim_get_current_buf()
    vim.bo[state.buffer].buftype = "nofile"
    vim.bo[state.buffer].bufhidden = "hide"
    vim.bo[state.buffer].swapfile = false
    vim.bo[state.buffer].filetype = "torio"
    vim.api.nvim_buf_set_name(state.buffer, "torio://panel")
  else
    vim.api.nvim_win_set_buf(state.window, state.buffer)
  end
  vim.wo[state.window].number = false
  vim.wo[state.window].relativenumber = false
  vim.wo[state.window].signcolumn = "no"
  vim.wo[state.window].wrap = false

  local map = function(lhs, rhs)
    vim.keymap.set("n", lhs, rhs, { buffer = state.buffer, silent = true, nowait = true })
  end
  map("q", function() vim.api.nvim_win_close(0, true) end)
  map("r", function()
	local project = selected_project()
	if state.view == "sessions" and project then
		show_sessions(project)
	elseif state.view == "health" and project then
		show_health(project)
	else
		refresh_projects()
	end
  end)
  map("b", render_projects)
  map("u", function() use_project(selected_project()) end)
  map("t", function() open_terminal(selected_project(), false) end)
  map("P", function() open_terminal(selected_project(), true) end)
  map("h", function() show_health(selected_project()) end)
  map("s", function() show_sessions(selected_project()) end)
  map("<CR>", function() show_sessions(selected_project()) end)
  map("y", function()
    local session = state.session_by_line[vim.api.nvim_win_get_cursor(0)[1]]
    if session and session.id then
      vim.fn.setreg("+", session.id)
      notify("copied session id")
    end
  end)
end

function M.open()
  ensure_panel()
  refresh_projects()
end

local function project_from_id(id)
  return { id = id, path = "/home/hermes/projects/" .. id }
end

function M.enter(id) open_terminal(project_from_id(id), false) end
function M.push_shell(id) open_terminal(project_from_id(id), true) end
function M.use(id) use_project(project_from_id(id)) end
function M.sessions(id)
  ensure_panel()
  show_sessions(project_from_id(id))
end
function M.health(id)
  ensure_panel()
  show_health(project_from_id(id))
end

local function create_commands()
  local complete = function()
    return vim.tbl_map(function(project) return project.id end, state.projects)
  end
  vim.api.nvim_create_user_command("Torio", M.open, {})
  vim.api.nvim_create_user_command("TorioProjects", M.open, {})
  vim.api.nvim_create_user_command("TorioUse", function(args) M.use(args.args) end, { nargs = 1, complete = complete })
  vim.api.nvim_create_user_command("TorioEnter", function(args) M.enter(args.args) end, { nargs = 1, complete = complete })
  vim.api.nvim_create_user_command("TorioPushShell", function(args) M.push_shell(args.args) end, { nargs = 1, complete = complete })
  vim.api.nvim_create_user_command("TorioSessions", function(args) M.sessions(args.args) end, { nargs = 1, complete = complete })
  vim.api.nvim_create_user_command("TorioHealth", function(args) M.health(args.args) end, { nargs = 1, complete = complete })
end

function M.setup(opts)
  config = vim.tbl_deep_extend("force", vim.deepcopy(defaults), opts or {})
  if vim.fn.exists(":Torio") == 0 then
    create_commands()
  end
end

return M
