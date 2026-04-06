import * as vscode from "vscode";
import {
  LanguageClient,
  type LanguageClientOptions,
  type ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export async function activate(
  context: vscode.ExtensionContext,
): Promise<void> {
  const config = vscode.workspace.getConfiguration("gastro");
  const customPath = config.get<string>("lspPath");

  const command = customPath || "gastro";
  const args = customPath ? [] : ["lsp"];

  const serverOptions: ServerOptions = {
    command,
    args,
    transport: TransportKind.stdio,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "gastro" }],
  };

  client = new LanguageClient(
    "gastro-lsp",
    "Gastro Language Server",
    serverOptions,
    clientOptions,
  );

  // Register notifications before start so none are missed during init
  client.onNotification(
    "gastro/goplsNotAvailable",
    async (_params: unknown) => {
      const action = await vscode.window.showWarningMessage(
        "Gopls is not installed. Go language features (completions, hover, " +
          "diagnostics) in the frontmatter will be limited.\n\n" +
          "Install gopls to enable full Go intelligence.",
        "Install gopls",
        "Dismiss",
      );

      if (action === "Install gopls") {
        const terminal = vscode.window.createTerminal("Install gopls");
        terminal.show();
        terminal.sendText("go install golang.org/x/tools/gopls@latest");

        const reload = await vscode.window.showInformationMessage(
          "After gopls finishes installing, reload the window to activate Go features.",
          "Reload Window",
        );
        if (reload === "Reload Window") {
          vscode.commands.executeCommand("workbench.action.reloadWindow");
        }
      }
    },
  );

  try {
    await client.start();
  } catch {
    vscode.window.showErrorMessage(
      `Gastro LSP failed to start. Is "gastro" installed and on your PATH?\n\n` +
        `Install with: go install github.com/andrioid/gastro/cmd/gastro@latest\n` +
        `Or: mise use github:andrioid/gastro@latest`,
    );
    return;
  }

  // Check if the gastro binary version matches the extension version.
  // Mismatches cause features like formatting or snippet completions to
  // silently not work because the binary doesn't advertise them yet.
  const serverVersion = (
    client.initializeResult as { serverInfo?: { version?: string } }
  )?.serverInfo?.version;
  const extensionVersion = context.extension.packageJSON.version as string;

  if (
    serverVersion &&
    serverVersion !== "dev" &&
    serverVersion !== extensionVersion
  ) {
    const action = await vscode.window.showWarningMessage(
      `Gastro version mismatch: the gastro binary is v${serverVersion} ` +
        `but the extension expects v${extensionVersion}. ` +
        `Some features may not work correctly.`,
      "Update gastro",
      "Dismiss",
    );
    if (action === "Update gastro") {
      const terminal = vscode.window.createTerminal("Update Gastro");
      terminal.show();
      terminal.sendText(
        "go install github.com/andrioid/gastro/cmd/gastro@latest",
      );
    }
  }
}

export function deactivate(): Thenable<void> | undefined {
  if (client?.isRunning()) {
    return client.stop();
  }
  return undefined;
}
