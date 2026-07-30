local root = assert(vim.env.TORIO_NVIM_ROOT)
vim.opt.runtimepath:prepend(root)

local tmp = vim.fn.tempname()
vim.fn.mkdir(tmp, "p")
local torio = tmp .. "/torio"
local curl = tmp .. "/curl"
local curl_args = tmp .. "/curl.args"
local curl_stdin = tmp .. "/curl.stdin"

vim.fn.writefile({
  "#!/bin/sh",
  "case \"$*\" in",
  "  'project list --json') printf '%s\\n' '{\"ok\":true,\"command\":\"project.list\",\"data\":{\"projects\":[{\"id\":\"torio\",\"display_name\":\"Torio\",\"path\":\"/home/hermes/projects/torio\",\"remote\":\"git@github.com:owner/torio.git\"}],\"count\":1}}' ;;",
  "  'project show torio --json') printf '%s\\n' '{\"ok\":true,\"command\":\"project.show\",\"data\":{\"id\":\"torio\",\"path\":\"/home/hermes/projects/torio\",\"checkout\":{\"path_exists\":true,\"repository\":true,\"origin_matches\":true,\"clean\":true,\"shared_permissions\":true},\"hermes\":{\"registered\":true}}}' ;;",
  "  'project use torio --json') printf '%s\\n' '{\"ok\":true,\"command\":\"project.use\",\"data\":{\"id\":\"torio\",\"active\":true}}' ;;",
  "  *) printf 'unexpected torio argv: %s\\n' \"$*\" >&2; exit 64 ;;",
  "esac",
}, torio)
vim.fn.writefile({
  "#!/bin/sh",
  "printf '%s\\n' \"$@\" > " .. vim.fn.shellescape(curl_args),
  "cat > " .. vim.fn.shellescape(curl_stdin),
  "printf '%s\\n' '{\"sessions\":[{\"id\":\"session-1\",\"title\":\"Neovim\\nsmoke session\",\"is_active\":true,\"updated_at\":\"2026-07-30\"}]}'",
}, curl)
vim.fn.setfperm(torio, "rwx------")
vim.fn.setfperm(curl, "rwx------")

local plugin = require("torio")
plugin.setup({
  torio_cmd = { torio },
  curl_cmd = { curl },
  session_token = function() return "smoke-secret" end,
})

assert(vim.fn.exists(":Torio") == 2, ":Torio command missing")
assert(vim.fn.exists(":TorioEnter") == 2, ":TorioEnter command missing")
assert(vim.fn.exists(":TorioPushShell") == 2, ":TorioPushShell command missing")

plugin.open()
assert(vim.wait(3000, function()
  return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n"):find("torio", 1, true) ~= nil
end), "project panel did not render")

plugin.sessions("torio")
assert(vim.wait(3000, function()
  return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n"):find("Neovim smoke session", 1, true) ~= nil
end), "session panel did not render")

local args = table.concat(vim.fn.readfile(curl_args), "\n")
local stdin = table.concat(vim.fn.readfile(curl_stdin), "\n")
assert(not args:find("smoke-secret", 1, true), "session token leaked into curl argv")
assert(stdin:find("X-Hermes-Session-Token: smoke-secret", 1, true), "session token was not sent through stdin")
assert(args:find("cwd_prefix=/home/hermes/projects/torio", 1, true), "workspace scope missing from sessions request")

vim.cmd("qa!")
