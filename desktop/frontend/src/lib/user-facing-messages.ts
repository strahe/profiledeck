import { translate } from "$lib/i18n";

export function profileWarningMessage(message: string): string {
	const normalized = message.toLowerCase();
	if (normalized.includes("unsupported target")) {
		return translate("warnings.profile.unsupportedData");
	}
	if (normalized.includes("config") || normalized.includes("setting")) {
		return translate("warnings.profile.settingsUnavailable");
	}
	if (normalized.includes("login") || normalized.includes("auth")) {
		return translate("warnings.profile.loginUnavailable");
	}
	return translate("warnings.profile.unknown");
}

export function switchWarningMessage(message: string): string {
	const normalized = message.toLowerCase();
	if (normalized.includes("shared") && normalized.includes("login")) return translate("warnings.change.sharedLogin");
	if (normalized.includes("symlink")) return translate("warnings.switch.symlink");
	if (normalized.includes("claude code")) {
		if (normalized.includes("different claude_config_dir") || normalized.includes("saved target")) return translate("warnings.claudeCode.savedTarget");
		if (normalized.includes("expiry could not be determined")) return translate("warnings.claudeCode.expiryUnknown");
		if (normalized.includes("selected") && normalized.includes("expired")) return translate("warnings.claudeCode.selectedExpired");
		if (normalized.includes("observes") && normalized.includes("override")) return translate("warnings.claudeCode.authOverride");
		if (normalized.includes("restart") && normalized.includes("session")) return translate("warnings.claudeCode.restart");
		if (normalized.includes("apikeyhelper") || normalized.includes("authentication precedence")) return translate("warnings.claudeCode.precedence");
		if (normalized.includes("will not overwrite") || normalized.includes("will not be captured")) return translate("warnings.claudeCode.currentNotCaptured");
		if (normalized.includes("permissions will be repaired")) return translate("warnings.claudeCode.modeRepair");
		if (normalized.includes("run claude code /login")) return translate("warnings.claudeCode.loginRequired");
	}
	if (normalized.includes("toml semantic rewrite")) return translate("warnings.switch.tomlFormatting");
	if (normalized.includes("auth") || normalized.includes("login")) {
		return translate("warnings.switch.loginNotSaved");
	}
	if (normalized.includes("config")) return translate("warnings.switch.settingsNotSaved");
	return translate("warnings.switch.unknown");
}

export function profileChangeWarningMessage(message: string): string {
	const normalized = message.toLowerCase();
	if (normalized.includes("shared") && normalized.includes("login")) return translate("warnings.change.sharedLogin");
	if (normalized.includes("shared codex config")) return translate("warnings.change.sharedSettings");
	return profileWarningMessage(message);
}

export function joinUserMessages(messages: string[] | null | undefined, format: (message: string) => string): string {
	return [...new Set((messages ?? []).map(format))].join(" ");
}
