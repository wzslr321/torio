# torio.nvim

A dependency-free Neovim management panel for Torio projects. It calls Torio's
JSON CLI contract and never reads Torio or backend state files.

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

Neovim 0.10 or newer and `torio` are required. Nothing else: every backend is a
per-session process, so the panel has no endpoint to query and holds no
credential of any kind.

## Commands

- `:Torio` or `:TorioProjects`: open the panel.
- `:TorioEnter <id>`: open a routine terminal without SSH agent forwarding.
- `:TorioPushShell <id>`: open the explicit push-capable operator shell.
- `:TorioHealth <id>`: show checkout health.

Panel keys:

- `<CR>` or `t`: routine project terminal
- `P`: push-capable operator shell
- `h`: project health
- `r`: refresh
- `b`: projects
- `q`: close

The uppercase `P` is intentional. Routine terminals use `torio project enter`;
only `P`/`:TorioPushShell` crosses the Git write-capability boundary.
