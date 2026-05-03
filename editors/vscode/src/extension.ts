import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import {
  LanguageClient,
  type LanguageClientOptions,
  type ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

interface ResolvedServer {
  command: string;
  args: string[];
  cwd?: string;
  /** Human-readable label describing how the LSP was launched, for logs. */
  source: "lspPath setting" | "go tool (project-pinned)" | "PATH";
}

/**
 * Decide how to launch `gastro lsp`. Resolution order:
 *
 *   1. `gastro.lspPath` setting (explicit override -- launched as-is, no args).
 *   2. `go tool gastro lsp` -- if the workspace's `go.mod` contains
 *      `tool github.com/andrioid/gastro/cmd/gastro`, use the project-pinned
 *      version. Spawned with cwd = the directory containing that `go.mod`
 *      so `go tool` can resolve the module.
 *   3. `gastro` from PATH (legacy default).
 *
 * The `go tool` probe walks up from the workspace root, supporting both
 * single-line `tool foo` and grouped `tool ( ... )` directives.
 */
function resolveServerCommand(): ResolvedServer {
  const config = vscode.workspace.getConfiguration("gastro");
  const customPath = config.get<string>("lspPath");

  if (customPath && customPath.trim() !== "") {
    return { command: customPath, args: [], source: "lspPath setting" };
  }

  const folders = vscode.workspace.workspaceFolders;
  if (folders && folders.length > 0) {
    for (const folder of folders) {
      const goModDir = findGoModWithGastroTool(folder.uri.fsPath);
      if (goModDir) {
        return {
          command: "go",
          args: ["tool", "gastro", "lsp"],
          cwd: goModDir,
          source: "go tool (project-pinned)",
        };
      }
    }
  }

  return { command: "gastro", args: ["lsp"], source: "PATH" };
}

/**
 * Walk up from `start` looking for a `go.mod` that declares the gastro CLI
 * as a tool dependency. Returns the directory containing that `go.mod`,
 * or undefined if none is found. Stops at the filesystem root.
 */
function findGoModWithGastroTool(start: string): string | undefined {
  let dir = start;
  // Bound the walk to avoid pathological cases. 32 is more than enough for
  // any sane project layout.
  for (let i = 0; i < 32; i++) {
    const candidate = path.join(dir, "go.mod");
    try {
      const stat = fs.statSync(candidate);
      if (stat.isFile()) {
        const content = fs.readFileSync(candidate, "utf8");
        if (goModDeclaresGastroTool(content)) {
          return dir;
        }
        // Found a go.mod but it doesn't pin gastro -- this is the module
        // boundary; don't keep walking into a parent module.
        return undefined;
      }
    } catch {
      // ENOENT or other -- keep walking up.
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      return undefined;
    }
    dir = parent;
  }
  return undefined;
}

/**
 * Match either:
 *   tool github.com/andrioid/gastro/cmd/gastro
 * or:
 *   tool (
 *     github.com/andrioid/gastro/cmd/gastro
 *   )
 *
 * Comments and other tools in the block are tolerated.
 */
function goModDeclaresGastroTool(content: string): boolean {
  const target = "github.com/andrioid/gastro/cmd/gastro";
  const lines = content.split(/\r?\n/);
  let inToolBlock = false;
  for (const rawLine of lines) {
    const line = rawLine.replace(/\/\/.*$/, "").trim();
    if (line === "") continue;

    if (inToolBlock) {
      if (line.startsWith(")")) {
        inToolBlock = false;
        continue;
      }
      if (line === target) return true;
      continue;
    }

    if (line.startsWith("tool ")) {
      const rest = line.slice("tool ".length).trim();
      if (rest === "(") {
        inToolBlock = true;
        continue;
      }
      if (rest === target) return true;
    } else if (line === "tool (") {
      inToolBlock = true;
    }
  }
  return false;
}

export async function activate(
  context: vscode.ExtensionContext,
): Promise<void> {
  const resolved = resolveServerCommand();

  const serverOptions: ServerOptions = {
    command: resolved.command,
    args: resolved.args,
    options: resolved.cwd ? { cwd: resolved.cwd } : undefined,
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

  // Surface how the LSP was launched in the output channel so users can
  // verify whether they're getting the project-pinned binary or the one
  // on PATH. Mirrors the LSP server's own startup log line.
  client.outputChannel.appendLine(
    `[gastro-vscode] launching LSP via ${resolved.source}: ` +
      `${resolved.command} ${resolved.args.join(" ")}` +
      (resolved.cwd ? ` (cwd: ${resolved.cwd})` : ""),
  );

  // Register notifications before start so none are missed during init
  client.onNotification(
    "gastro/goNotAvailable",
    async (params: { message?: string }) => {
      const action = await vscode.window.showWarningMessage(
        params.message ||
          "Go is not installed. Gastro can edit .gastro files, but building requires Go.",
        "Install Go",
        "Dismiss",
      );

      if (action === "Install Go") {
        vscode.env.openExternal(vscode.Uri.parse("https://go.dev/dl/"));
      }
    },
  );

  client.onNotification(
    "gastro/goplsNotAvailable",
    async (params: { message?: string }) => {
      const action = await vscode.window.showWarningMessage(
        params.message ||
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
    const action = await vscode.window.showErrorMessage(
      `Gastro LSP failed to start. Install gastro one of three ways:\n\n` +
        `  • Per-project (recommended for teams): ` +
        `go get -tool github.com/andrioid/gastro/cmd/gastro (then reload)\n` +
        `  • Global with mise: mise use github:andrioid/gastro@latest\n` +
        `  • Global with go install: ` +
        `go install github.com/andrioid/gastro/cmd/gastro@latest`,
      "Install gastro (go install)",
      "Reload Window",
    );

    if (action === "Install gastro (go install)") {
      const terminal = vscode.window.createTerminal("Install Gastro");
      terminal.show();
      terminal.sendText(
        "go install github.com/andrioid/gastro/cmd/gastro@latest",
      );
    } else if (action === "Reload Window") {
      vscode.commands.executeCommand("workbench.action.reloadWindow");
    }
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
    const launchedViaGoTool = resolved.source === "go tool (project-pinned)";
    const message = launchedViaGoTool
      ? `Gastro version mismatch: the LSP is v${serverVersion} (pinned via ` +
        `\`go tool\` in your project's go.mod) but the extension is ` +
        `v${extensionVersion}. To realign, update the pin with ` +
        `\`go get -tool github.com/andrioid/gastro/cmd/gastro@latest\` ` +
        `or downgrade the extension. Some features may not work correctly ` +
        `until the versions match.`
      : `Gastro version mismatch: the gastro binary is v${serverVersion} ` +
        `but the extension expects v${extensionVersion}. ` +
        `Some features may not work correctly.`;

    const action = await vscode.window.showWarningMessage(
      message,
      "Update gastro",
      "Dismiss",
    );
    if (action === "Update gastro") {
      const terminal = vscode.window.createTerminal("Update Gastro");
      terminal.show();
      terminal.sendText(
        launchedViaGoTool
          ? "go get -tool github.com/andrioid/gastro/cmd/gastro@latest"
          : "go install github.com/andrioid/gastro/cmd/gastro@latest",
      );

      const reload = await vscode.window.showInformationMessage(
        "After gastro finishes updating, reload the window to use the new version.",
        "Reload Window",
      );
      if (reload === "Reload Window") {
        vscode.commands.executeCommand("workbench.action.reloadWindow");
      }
    }
  }
}

export function deactivate(): Thenable<void> | undefined {
  if (client?.isRunning()) {
    return client.stop();
  }
  return undefined;
}
