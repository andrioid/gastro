const { workspace, window } = require("vscode");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");

let client;

function activate(context) {
  const config = workspace.getConfiguration("gastro");
  const customPath = config.get("lspPath");

  const command = customPath || "gastro";
  const args = customPath ? [] : ["lsp"];

  const serverOptions = {
    command,
    args,
    transport: TransportKind.stdio,
  };

  const clientOptions = {
    documentSelector: [{ scheme: "file", language: "gastro" }],
  };

  client = new LanguageClient(
    "gastro-lsp",
    "Gastro Language Server",
    serverOptions,
    clientOptions,
  );

  client.start().catch((err) => {
    const msg =
      `Gastro LSP failed to start. Is "gastro" installed and on your PATH?\n\n` +
      `Install with: go install github.com/andrioid/gastro/cmd/gastro@latest\n` +
      `Or: mise use github:andrioid/gastro@latest`;
    window.showErrorMessage(msg);
  });
}

function deactivate() {
  if (client && client.isRunning()) {
    return client.stop();
  }
}

module.exports = { activate, deactivate };
