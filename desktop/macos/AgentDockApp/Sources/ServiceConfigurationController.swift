import Darwin
import Foundation

struct EditableServiceSettings {
    let port: Int
    let logLevel: String
    let mcpAppsMode: MCPAppsMode
    let browserEnabled: Bool
    let browserCDPURL: String
    let browserReuseExistingCDP: Bool
    let codexHostEnabled: Bool
    let codexExecutablePath: String
    let codexHome: String
    let computerUseServer: String
    let acpEnabled: Bool
    let acpProfiles: [ACPProfileConfiguration]
    let acpDefaultProfile: String

    init(
        port: Int,
        logLevel: String,
        mcpAppsMode: MCPAppsMode,
        browserEnabled: Bool,
        browserCDPURL: String,
        browserReuseExistingCDP: Bool,
        codexHostEnabled: Bool = false,
        codexExecutablePath: String = "",
        codexHome: String = "",
        computerUseServer: String = "",
        acpEnabled: Bool,
        acpProfiles: [ACPProfileConfiguration] = [],
        acpDefaultProfile: String = ""
    ) {
        self.port = port
        self.logLevel = logLevel
        self.mcpAppsMode = mcpAppsMode
        self.browserEnabled = browserEnabled
        self.browserCDPURL = browserCDPURL
        self.browserReuseExistingCDP = browserReuseExistingCDP
        self.codexHostEnabled = codexHostEnabled
        self.codexExecutablePath = codexExecutablePath
        self.codexHome = codexHome
        self.computerUseServer = computerUseServer
        self.acpEnabled = acpEnabled
        self.acpProfiles = acpProfiles
        self.acpDefaultProfile = acpDefaultProfile
    }

    func validated() throws -> EditableServiceSettings {
        try ServicePortValidation.validate(port)
        let normalizedLogLevel = ServiceConfiguration.normalizedLogLevel(logLevel)
        guard ["debug", "info", "warn", "error"].contains(normalizedLogLevel) else {
            throw ValidationError(L10n.text("Log level must be debug, info, warn, or error."))
        }

        let browserCDPURL = try Self.normalizeBrowserCDPURL(browserCDPURL)
        if browserEnabled, browserCDPURL.isEmpty, !browserReuseExistingCDP, BrowserSupportController.detectExecutable() == nil {
            throw ValidationError(L10n.text("No supported Chrome, Chromium, or Microsoft Edge was detected and no external CDP is configured."))
        }

        let codexExecutablePath = try Self.normalizeAbsolutePath(codexExecutablePath, field: L10n.text("Codex executable path"))
        if codexHostEnabled, !codexExecutablePath.isEmpty {
            var isDirectory: ObjCBool = false
            guard FileManager.default.fileExists(atPath: codexExecutablePath, isDirectory: &isDirectory), !isDirectory.boolValue else {
                throw ValidationError(L10n.text("Codex executable path must point to an existing file."))
            }
        }
        let codexHome = try Self.normalizeAbsolutePath(codexHome, field: L10n.text("CODEX_HOME"))
        let computerUseServer = computerUseServer.trimmingCharacters(in: .whitespacesAndNewlines)

        return try validatedWithProfiles(
            normalizedLogLevel: normalizedLogLevel,
            browserCDPURL: browserCDPURL,
            codexExecutablePath: codexExecutablePath,
            codexHome: codexHome,
            computerUseServer: computerUseServer
        )
    }

    private func validatedWithProfiles(
        normalizedLogLevel: String,
        browserCDPURL: String,
        codexExecutablePath: String,
        codexHome: String,
        computerUseServer: String
    ) throws -> EditableServiceSettings {
        var seen = Set<String>()
        var profiles: [ACPProfileConfiguration] = []
        profiles.reserveCapacity(acpProfiles.count)

        for rawProfile in acpProfiles {
            var profile = rawProfile
            profile.id = profile.id.trimmingCharacters(in: .whitespacesAndNewlines)
            guard Self.validProfileID(profile.id) else {
                throw ValidationError(L10n.format("Invalid Coding Agent profile ID: %@", profile.id))
            }
            guard seen.insert(profile.id).inserted else {
                throw ValidationError(L10n.format("Duplicate Coding Agent profile ID: %@", profile.id))
            }
            if profile.kind == .custom {
                let displayName = profile.displayName?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                profile.displayName = displayName.isEmpty ? profile.id : displayName
            } else {
                profile.displayName = nil
            }
            switch profile.kind {
            case .codex, .claude, .grok:
                guard profile.id == profile.kind.rawValue else {
                    throw ValidationError(L10n.format("Built-in Coding Agent %@ must use profile ID %@.", profile.kind.title, profile.kind.rawValue))
                }
            case .custom:
                guard !["codex", "claude", "grok"].contains(profile.id) else {
                    throw ValidationError(L10n.format("Custom Coding Agent profile ID %@ is reserved.", profile.id))
                }
            }

            if acpEnabled, profile.enabled {
                let resolution = profile.kind.resolveAdapter(
                    configuredCommand: profile.command,
                    configuredArguments: profile.args
                )
                guard resolution.available else {
                    throw ValidationError(L10n.format("%@ is unavailable: %@.", profile.id, profile.kind.missingAdapterMessage))
                }
                profile.command = resolution.command
                profile.args = resolution.arguments
            }
            profiles.append(profile)
        }

        let enabledProfiles = profiles.filter(\.enabled)
        if acpEnabled, enabledProfiles.isEmpty {
            throw ValidationError(L10n.text("Enable at least one Coding Agent profile."))
        }
        let defaultProfileID = acpDefaultProfile.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? enabledProfiles.first?.id ?? profiles.first?.id ?? ""
            : acpDefaultProfile.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let defaultProfile = profiles.first(where: { $0.id == defaultProfileID }),
              !acpEnabled || defaultProfile.enabled else {
            throw ValidationError(L10n.text("The default Coding Agent profile must reference an enabled profile."))
        }

        return EditableServiceSettings(
            port: port,
            logLevel: normalizedLogLevel,
            mcpAppsMode: mcpAppsMode,
            browserEnabled: browserEnabled,
            browserCDPURL: browserCDPURL,
            browserReuseExistingCDP: browserReuseExistingCDP,
            codexHostEnabled: codexHostEnabled,
            codexExecutablePath: codexExecutablePath,
            codexHome: codexHome,
            computerUseServer: computerUseServer,
            acpEnabled: acpEnabled,
            acpProfiles: profiles,
            acpDefaultProfile: defaultProfileID
        )
    }

    private static func validProfileID(_ value: String) -> Bool {
        guard !value.isEmpty, value.utf8.count <= 64 else { return false }
        return value.unicodeScalars.allSatisfy { scalar in
            let value = scalar.value
            return (65...90).contains(value)
                || (97...122).contains(value)
                || (48...57).contains(value)
                || value == 46
                || value == 95
                || value == 45
        }
    }

    /// 空值表示“使用默认发现”，此时不写入环境变量；非空则必须绝对，避免 GUI
    /// 保存相对路径后依赖服务进程的工作目录。
    private static func normalizeAbsolutePath(_ raw: String, field: String) throws -> String {
        let candidate = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !candidate.isEmpty else { return "" }
        guard candidate.hasPrefix("/") else {
            throw ValidationError(L10n.format("%@ must be an absolute path.", field))
        }
        return candidate
    }

    private static func normalizeBrowserCDPURL(_ raw: String) throws -> String {
        let candidate = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !candidate.isEmpty else { return "" }
        guard let components = URLComponents(string: candidate),
              let scheme = components.scheme?.lowercased(),
              ["http", "https", "ws", "wss"].contains(scheme),
              components.host?.isEmpty == false else {
            throw ValidationError(L10n.text("Browser CDP address must be a valid HTTP(S) or WS(S) address."))
        }
        guard components.user == nil,
              components.password == nil,
              components.fragment == nil else {
            throw ValidationError(L10n.text("Browser CDP address cannot contain credentials or a fragment."))
        }
        return candidate
    }
}

final class ServiceConfigurationController {
    private let service: ServiceController
    private let fileManager = FileManager.default

    init(service: ServiceController) {
        self.service = service
    }

    func apply(_ requested: EditableServiceSettings) async throws {
        let settings = try requested.validated()
        try await service.withLifecycleTransaction { lifecycle in
            try await self.applyWithinLifecycle(settings, lifecycle: lifecycle)
        }
    }

    private func applyWithinLifecycle(
        _ settings: EditableServiceSettings,
        lifecycle: ServiceController.LifecycleTransaction
    ) async throws {
        let environmentURL = service.paths.environment
        let originalData = try readPrivateRegularFile(environmentURL)
        let environment = try ManagedEnvironment.load(from: environmentURL)
        let replacements = [
            "AGENTDOCK_PORT": String(settings.port),
            "AGENTDOCK_LOG_LEVEL": settings.logLevel,
            "AGENTDOCK_MCP_APPS_MODE": settings.mcpAppsMode.rawValue,
            "AGENTDOCK_BROWSER_ENABLED": settings.browserEnabled ? "true" : "false",
            "AGENTDOCK_BROWSER_CDP_URL": settings.browserCDPURL,
            "AGENTDOCK_BROWSER_REUSE_EXISTING_CDP": settings.browserReuseExistingCDP ? "true" : "false",
            "AGENTDOCK_CODEX_HOST_ENABLED": settings.codexHostEnabled ? "true" : "false",
            "AGENTDOCK_CODEX_EXECUTABLE_PATH": settings.codexExecutablePath,
            "AGENTDOCK_CODEX_HOME": settings.codexHome,
            "AGENTDOCK_COMPUTER_USE_SERVER": settings.computerUseServer,
            "AGENTDOCK_ACP_ENABLED": settings.acpEnabled ? "true" : "false",
            "AGENTDOCK_ACP_PROFILES_JSON": try ACPDesktopConfiguration.encodeProfiles(settings.acpProfiles),
            "AGENTDOCK_ACP_DEFAULT_PROFILE": settings.acpDefaultProfile,
        ]
        let updatedData = try environment.dataByUpdating(replacements, removing: ServiceConfiguration.removableLegacyKeys)
        let wasLoaded = service.isLoaded()

        try await service.runInBackground {
            try self.writePrivateAtomically(updatedData, to: environmentURL)
        }
        guard wasLoaded else { return }

        do {
            _ = try await lifecycle.restart()
        } catch {
            let originalError = error
            do {
                try await service.runInBackground {
                    try self.writePrivateAtomically(originalData, to: environmentURL)
                }
                _ = try await lifecycle.restart()
            } catch {
                throw ValidationError(L10n.format(
                    "Failed to start the new configuration, and validation after restoring the old configuration also failed: %@",
                    error.localizedDescription
                ))
            }
            throw ValidationError(L10n.format(
                "Failed to start the new configuration; restored the previous configuration: %@",
                originalError.localizedDescription
            ))
        }
    }

    private func readPrivateRegularFile(_ url: URL) throws -> Data {
        let values = try url.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true else {
            throw ValidationError(L10n.text("AgentDock configuration must be a regular file, not a symbolic link."))
        }
        let attributes = try fileManager.attributesOfItem(atPath: url.path)
        if let owner = attributes[.ownerAccountID] as? NSNumber,
           owner.uint32Value != getuid() {
            throw ValidationError(L10n.text("AgentDock configuration file does not belong to the current user."))
        }
        if let permissions = attributes[.posixPermissions] as? NSNumber,
           permissions.intValue & 0o077 != 0 {
            throw ValidationError(L10n.text("AgentDock configuration file permissions are too broad. Restore them to 0600 first."))
        }
        return try Data(contentsOf: url)
    }

    private func writePrivateAtomically(_ data: Data, to url: URL) throws {
        let directory = url.deletingLastPathComponent()
        try fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
        let temporary = directory.appendingPathComponent(".\(url.lastPathComponent).tmp.\(UUID().uuidString)")
        defer { try? fileManager.removeItem(at: temporary) }
        guard fileManager.createFile(
            atPath: temporary.path,
            contents: data,
            attributes: [.posixPermissions: 0o600]
        ) else {
            throw ValidationError(L10n.text("Unable to create the temporary AgentDock configuration file."))
        }
        try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: temporary.path)
        if Darwin.rename(temporary.path, url.path) != 0 {
            throw ValidationError(L10n.format(
                "Unable to atomically replace AgentDock configuration: %@",
                String(cString: strerror(errno))
            ))
        }
    }
}
