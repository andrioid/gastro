use std::fs;
use zed_extension_api::{self as zed, LanguageServerId};

struct GastroExtension {
    cached_binary_path: Option<String>,
}

impl GastroExtension {
    fn language_server_binary_path(
        &mut self,
        language_server_id: &LanguageServerId,
    ) -> zed::Result<String> {
        // Return cached path if binary still exists
        if let Some(ref path) = self.cached_binary_path {
            if fs::metadata(path).map_or(false, |m| m.is_file()) {
                return Ok(path.clone());
            }
        }

        let (os, arch) = zed::current_platform();

        let os_str = match os {
            zed::Os::Mac => "darwin",
            zed::Os::Linux => "linux",
            zed::Os::Windows => "windows",
        };

        let arch_str = match arch {
            zed::Architecture::Aarch64 => "arm64",
            zed::Architecture::X8664 => "amd64",
            zed::Architecture::X86 => "amd64",
        };

        let ext = if matches!(os, zed::Os::Windows) {
            ".exe"
        } else {
            ""
        };

        let binary_name = format!("gastro-lsp-{os_str}-{arch_str}{ext}");
        let binary_path = format!("gastro-lsp{ext}");

        zed::set_language_server_installation_status(
            language_server_id,
            &zed::LanguageServerInstallationStatus::CheckingForUpdate,
        );

        let release = zed::latest_github_release(
            "andrioid/gastro",
            zed::GithubReleaseOptions {
                require_assets: true,
                pre_release: false,
            },
        )
        .map_err(|e| format!("failed to fetch latest release: {e}"))?;

        let asset = release
            .assets
            .iter()
            .find(|a| a.name == binary_name)
            .ok_or_else(|| {
                format!(
                    "no matching binary '{binary_name}' in release {}",
                    release.version
                )
            })?;

        zed::set_language_server_installation_status(
            language_server_id,
            &zed::LanguageServerInstallationStatus::Downloading,
        );

        zed::download_file(
            &asset.download_url,
            &binary_path,
            zed::DownloadedFileType::Uncompressed,
        )
        .map_err(|e| format!("failed to download {binary_name}: {e}"))?;

        zed::make_file_executable(&binary_path)
            .map_err(|e| format!("failed to make {binary_path} executable: {e}"))?;

        self.cached_binary_path = Some(binary_path.clone());
        Ok(binary_path)
    }
}

impl zed::Extension for GastroExtension {
    fn new() -> Self {
        Self {
            cached_binary_path: None,
        }
    }

    fn language_server_command(
        &mut self,
        language_server_id: &LanguageServerId,
        _worktree: &zed::Worktree,
    ) -> zed::Result<zed::Command> {
        let binary_path = self.language_server_binary_path(language_server_id)?;
        Ok(zed::Command {
            command: binary_path,
            args: vec![],
            env: vec![],
        })
    }
}

zed::register_extension!(GastroExtension);
