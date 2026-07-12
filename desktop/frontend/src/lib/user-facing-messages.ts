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
	if (normalized.includes("symlink")) return translate("warnings.switch.symlink");
	if (normalized.includes("toml semantic rewrite")) return translate("warnings.switch.tomlFormatting");
	if (normalized.includes("auth") || normalized.includes("login")) {
		return translate("warnings.switch.loginNotSaved");
	}
	if (normalized.includes("config")) return translate("warnings.switch.settingsNotSaved");
	return translate("warnings.switch.unknown");
}

export function profileChangeWarningMessage(message: string): string {
	const normalized = message.toLowerCase();
	if (normalized.includes("shared codex login")) return translate("warnings.change.sharedLogin");
	if (normalized.includes("shared codex config")) return translate("warnings.change.sharedSettings");
	return profileWarningMessage(message);
}

export function joinUserMessages(messages: string[] | null | undefined, format: (message: string) => string): string {
	return [...new Set((messages ?? []).map(format))].join(" ");
}
