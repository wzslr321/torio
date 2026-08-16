local root = assert(vim.env.TORIO_NVIM_ROOT)
vim.opt.runtimepath:prepend(root)

local tmp = vim.fn.tempname()
vim.fn.mkdir(tmp, "p")
local torio = tmp .. "/torio"

vim.fn.writefile({
  "#!/bin/sh",
  "case \"$*\" in",
  "  'project list --json') printf '%s\\n' '{\"ok\":true,\"command\":\"project.list\",\"data\":{\"projects\":[{\"id\":\"torio\",\"display_name\":\"Torio\",\"path\":\"/home/claude/projects/torio\",\"remote\":\"git@github.com:owner/torio.git\"}],\"count\":1}}' ;;",
  "  'project show torio --json') printf '%s\\n' '{\"ok\":true,\"command\":\"project.show\",\"data\":{\"id\":\"torio\",\"path\":\"/home/claude/projects/torio\",\"checkout\":{\"path_exists\":true,\"repository\":true,\"origin_matches\":true,\"clean\":true,\"shared_permissions\":true}}}' ;;",
  "  *) printf 'unexpected torio argv: %s\\n' \"$*\" >&2; exit 64 ;;",
  "esac",
}, torio)
vim.fn.setfperm(torio, "rwx------")

local plugin = require("torio")
plugin.setup({ torio_cmd = { torio } })

assert(vim.fn.exists(":Torio") == 2, ":Torio command missing")
assert(vim.fn.exists(":TorioEnter") == 2, ":TorioEnter command missing")
assert(vim.fn.exists(":TorioPushShell") == 2, ":TorioPushShell command missing")
assert(vim.fn.exists(":TorioHealth") == 2, ":TorioHealth command missing")

-- The panel drives the JSON CLI contract and nothing else. There is no HTTP
-- client any more: every backend is a per-session process, so there is no
-- endpoint to query and no session token to hold.
assert(vim.fn.exists(":TorioSessions") == 0, ":TorioSessions outlived the service backend")
assert(vim.fn.exists(":TorioUse") == 0, ":TorioUse outlived the project registry")

plugin.open()
assert(vim.wait(3000, function()
  return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n"):find("torio", 1, true) ~= nil
end), "project panel did not render")

plugin.health("torio")
assert(vim.wait(3000, function()
  return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n"):find("origin_matches", 1, true) ~= nil
end), "health panel did not render")

vim.cmd("qa!")
