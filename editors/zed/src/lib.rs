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

        let archive_name = format!("gastro-{os_str}-{arch_str}");
        let archive_type = if matches!(os, zed::Os::Windows) {
            zed::DownloadedFileType::Zip
        } else {
            zed::DownloadedFileType::GzipTar
        };
        let archive_ext = if matches!(os, zed::Os::Windows) {
            ".zip"
        } else {
            ".tar.gz"
        };
        let binary_path = format!("gastro{ext}");

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

        let asset_name = format!("{archive_name}{archive_ext}");
        let asset = release
            .assets
            .iter()
            .find(|a| a.name == asset_name)
            .ok_or_else(|| {
                format!(
                    "no matching archive '{asset_name}' in release {}",
                    release.version
                )
            })?;

        zed::set_language_server_installation_status(
            language_server_id,
            &zed::LanguageServerInstallationStatus::Downloading,
        );

        zed::download_file(&asset.download_url, &binary_path, archive_type)
            .map_err(|e| format!("failed to download {asset_name}: {e}"))?;

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
            args: vec!["lsp".to_string()],
            env: vec![],
        })
    }
}

zed::register_extension!(GastroExtension);
