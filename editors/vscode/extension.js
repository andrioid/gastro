const { workspace, window, commands } = require("vscode");
const {
  LanguageClient,
  TransportKind,
} = require("vscode-languageclient/node");

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

  // Handle custom notification when gopls is not available
  client.onReady().then(() => {
    client.onNotification("gastro/goplsNotAvailable", async (params) => {
      const action = await window.showWarningMessage(
        "Gopls is not installed. Go language features (completions, hover, " +
          "diagnostics) in the frontmatter will be limited.\n\n" +
          "Install gopls to enable full Go intelligence.",
        "Install gopls",
        "Dismiss",
      );

      if (action === "Install gopls") {
        const terminal = window.createTerminal("Install gopls");
        terminal.show();
        terminal.sendText("go install golang.org/x/tools/gopls@latest");

        const reload = await window.showInformationMessage(
          "After gopls finishes installing, reload the window to activate Go features.",
          "Reload Window",
        );
        if (reload === "Reload Window") {
          commands.executeCommand("workbench.action.reloadWindow");
        }
      }
    });
  });
}

function deactivate() {
  if (client && client.isRunning()) {
    return client.stop();
  }
}

module.exports = { activate, deactivate };
