-- Neovim plugin for Gastro (.gastro) files
-- Provides tree-sitter highlighting and LSP integration.
--
-- Installation:
-- 1. Copy this file to ~/.config/nvim/after/plugin/gastro.lua
-- 2. Ensure gastro-lsp is in your PATH
-- 3. Ensure tree-sitter-gastro is installed

local M = {}

-- Register .gastro filetype
vim.filetype.add({
  extension = {
    gastro = "gastro",
  },
})

-- Configure tree-sitter parser (requires nvim-treesitter)
local ok, parsers = pcall(require, "nvim-treesitter.parsers")
if ok then
  local parser_config = parsers.get_parser_configs()
  parser_config.gastro = {
    install_info = {
      url = "https://github.com/andrioid/gastro",
      files = { "tree-sitter-gastro/src/parser.c" },
      branch = "main",
    },
    filetype = "gastro",
  }
end

-- Configure LSP client
function M.setup(opts)
  opts = opts or {}

  vim.api.nvim_create_autocmd("FileType", {
    pattern = "gastro",
    callback = function()
      vim.lsp.start({
        name = "gastro-lsp",
        cmd = { opts.cmd or "gastro-lsp" },
        root_dir = vim.fs.dirname(
          vim.fs.find({ "go.mod" }, { upward = true })[1]
        ),
      })
    end,
  })
end

return M
