if vim.fn.has("nvim-0.10") == 0 then
  vim.notify("torio.nvim requires Neovim 0.10 or newer", vim.log.levels.ERROR)
  return
end

require("torio").setup()
