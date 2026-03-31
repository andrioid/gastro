const path = require("path");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");

let client;

function activate(context) {
  const serverPath = context.asAbsolutePath(path.join("bin", "gastro-lsp"));

  const serverOptions = {
    command: serverPath,
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

  client.start();
}

function deactivate() {
  if (client && client.isRunning()) {
    return client.stop();
  }
}

module.exports = { activate, deactivate };
