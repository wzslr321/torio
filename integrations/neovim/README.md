# torio.nvim

A dependency-free Neovim management panel for Torio projects and Hermes sessions.
It calls Torio's JSON CLI contract and never reads Torio or Hermes state files.

## Install

With lazy.nvim from a Torio checkout:

```lua
{
  dir = "/path/to/torio/integrations/neovim",
  config = function()
    require("torio").setup()
  end,
}
```

Neovim 0.10 or newer, `torio`, and `curl` are required. The Hermes session view
also requires an active Torio tunnel and a session token. By default the token is
read from `HERMES_SESSION_TOKEN`; it is passed to curl through stdin, never as a
process argument.

For a keychain-backed token, return it from a callback rather than writing it in
this repository:

```lua
require("torio").setup({
  session_token = function()
    return vim.trim(vim.fn.system({ "security", "find-generic-password", "-w", "-s", "torio-hermes" }))
  end,
})
```

## Commands

- `:Torio` or `:TorioProjects`: open the panel.
- `:TorioUse <id>`: make a project active in Hermes.
- `:TorioEnter <id>`: open a routine terminal without SSH agent forwarding.
- `:TorioPushShell <id>`: open the explicit push-capable operator shell.
- `:TorioSessions <id>`: list Hermes sessions scoped to the project's workspace.
- `:TorioHealth <id>`: show checkout and Hermes registration health.

Panel keys:

- `<CR>` or `s`: project sessions
- `t`: routine project terminal
- `P`: push-capable operator shell
- `u`: activate project
- `h`: project health
- `r`: refresh
- `b`: projects
- `y`: copy selected session id
- `q`: close

The uppercase `P` is intentional. Routine terminals use `torio project enter`;
only `P`/`:TorioPushShell` crosses the Git write-capability boundary.
