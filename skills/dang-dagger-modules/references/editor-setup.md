# Dang Editor Setup

## Zed

The Dang language has a built-in LSP (`dang --lsp`) and a Zed extension at `codeberg.org/vito/zed-dang`.

### Install the dang binary

```sh
GOBIN=~/.local/bin go install github.com/vito/dang/cmd/dang@main
```

### Install the Zed extension

In Zed: Extensions > search "Dang" > Install.

### Configure Zed LSP

Add to `~/.config/zed/settings.json`:

```json
{
  "lsp": {
    "dang-lsp": {
      "binary": {
        "path": "dang",
        "arguments": ["--lsp"]
      }
    }
  }
}
```

### Troubleshooting

If syntax highlighting is missing after a Zed update or extension install:

1. Open command palette (`Cmd+Shift+P`)
2. Go to Extensions, find "Dang"
3. Click **Rebuild** to recompile the tree-sitter grammar WASM
4. Reopen the `.dang` file

## VS Code

There is a VS Code extension at `editors/vscode` in the `github.com/vito/dang` repo.

## Neovim

There is a Neovim plugin at `github.com/vito/dang.nvim`.
