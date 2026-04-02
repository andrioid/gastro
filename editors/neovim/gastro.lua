-- Neovim plugin for Gastro (.gastro) files
-- Provides tree-sitter highlighting and LSP integration.
--
-- Installation:
-- 1. Copy or symlink this file to ~/.config/nvim/after/plugin/gastro.lua
--    (or use `mise run link:neovim`)
-- 2. Ensure gastro-lsp is in your PATH (mise install provides it)
--
-- The LSP starts automatically for .gastro files. To customize the LSP
-- command, call require("gastro").setup({ cmd = "/path/to/gastro-lsp" })
-- in your Neovim config.

local M = {}

local lsp_cmd = "gastro-lsp"

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

-- Start LSP automatically for .gastro files
local group = vim.api.nvim_create_augroup("GastroLsp", { clear = true })

local function create_lsp_autocmd()
  vim.api.nvim_clear_autocmds({ group = group })
  vim.api.nvim_create_autocmd("FileType", {
    group = group,
    pattern = "gastro",
    callback = function()
      vim.lsp.start({
        name = "gastro-lsp",
        cmd = { lsp_cmd },
        root_dir = vim.fs.dirname(
          vim.fs.find({ "go.mod" }, { upward = true })[1]
        ),
      })
    end,
  })
end

create_lsp_autocmd()

-- Allow overriding the LSP command after the fact
function M.setup(opts)
  opts = opts or {}
  if opts.cmd then
    lsp_cmd = opts.cmd
  end
  create_lsp_autocmd()
end

return M
