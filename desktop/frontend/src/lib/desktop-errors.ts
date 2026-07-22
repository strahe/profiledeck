import { CancelError } from "@wailsio/runtime";

import { translate } from "$lib/i18n";

type DesktopErrorShape = {
	Code?: string;
	Message?: string;
	Details?: Record<string, unknown> | null;
	message?: string;
	code?: string;
	details?: Record<string, unknown> | null;
	name?: string;
	cause?: unknown;
	Cause?: unknown;
};

const userErrorKeys: Record<string, string> = {
	INVALID_RUNTIME_PATH: "errors.dataFolderInvalid",
	RUNTIME_INIT_FAILED: "errors.localDataUnavailable",
	STORE_INIT_FAILED: "errors.localDataUnavailable",
	STORE_OPEN_FAILED: "errors.localDataUnavailable",
	STORE_MIGRATION_FAILED: "errors.localDataUnavailable",
	STORE_NOT_INITIALIZED: "errors.dataNotInitialized",
	STORE_SCHEMA_INVALID: "errors.localDataUnavailable",
	STORE_SCHEMA_UNSUPPORTED: "errors.localDataUnsupported",
	STORE_STATUS_FAILED: "errors.localDataUnavailable",
	CONFIRMATION_REQUIRED: "errors.confirmationRequired",
	PROVIDER_EXISTS: "errors.providerExists",
	PROVIDER_IN_USE: "errors.providerInUse",
	PROVIDER_INVALID: "errors.providerInvalid",
	PROVIDER_NOT_FOUND: "errors.providerNotFound",
	PROVIDER_DISABLED: "errors.providerDisabled",
	PROFILE_EXISTS: "errors.profileExists",
	PROFILE_IN_USE: "errors.profileInUse",
	PROFILE_INVALID: "errors.profileInvalid",
	PROFILE_NOT_FOUND: "errors.profileNotFound",
	PLAN_BUILD_FAILED: "errors.switchPreviewFailed",
	ADAPTER_NOT_FOUND: "errors.providerInvalid",
	TARGET_INVALID: "errors.switchUnsupported",
	TARGET_EXISTS: "errors.fileAlreadyManaged",
	TARGET_NOT_FOUND: "errors.fileNotFound",
	TARGET_READ_FAILED: "errors.fileReadFailed",
	LOCK_ACQUIRE_FAILED: "errors.switchBusy",
	SWITCH_PLAN_UNSUPPORTED: "errors.switchUnsupported",
	TARGET_CHANGED: "errors.targetChanged",
	BACKUP_FAILED: "errors.backupFailed",
	BACKUP_INVALID: "errors.backupInvalid",
	BACKUP_SCHEMA_UNSUPPORTED: "errors.backupSchemaUnsupported",
	BACKUP_NOT_FOUND: "errors.backupNotFound",
	RESTORE_FAILED: "errors.restoreFailed",
	APPLICATION_RESTART_FAILED: "errors.applicationRestartFailed",
	RECOVERY_UNSUPPORTED: "errors.recoveryUnavailable",
	TARGET_WRITE_FAILED: "errors.fileWriteFailed",
	LOCK_REPAIR_UNSAFE: "errors.switchRepairUnsafe",
	USAGE_INVALID: "errors.usageUnavailable",
	USAGE_IMPORT_FAILED: "errors.usageUnavailable",
	USAGE_MIGRATION_REQUIRED: "errors.usageUnavailable",
	USAGE_SYNC_CONFLICT: "errors.usageUnavailable",
	CODEX_INVALID: "errors.codexNotReady",
	ANTIGRAVITY_INVALID: "errors.antigravityNotReady",
	CLAUDE_CODE_INVALID: "errors.claudeCodeNotReady",
	SETTING_INVALID: "errors.settingInvalid",
	EXPORT_FAILED: "errors.exportFailed",
	IMPORT_INVALID: "errors.importInvalid",
	IMPORT_CONFLICT: "errors.importConflict",
	IMPORT_PLAN_CHANGED: "errors.importChanged",
	UPDATE_CHANNEL_BUSY: "errors.updateChannelBusy",
	UPDATE_NOT_READY: "errors.updateNotReady",
	UPDATE_RESTART_FAILED: "errors.updateRestartFailed",
	OPERATION_CREATE_FAILED: "errors.operationFailed",
	OPERATION_UPDATE_FAILED: "errors.operationFailed",
	COMMAND_FAILED: "errors.operationFailed",
	DESKTOP_ERROR: "errors.operationFailed",
};

function userErrorMessage(code: string, fallback: string): string {
	if (code === "CANCELED") return "";
	const key = userErrorKeys[code];
	return key ? translate(key) : fallback;
}

export function desktopErrorCode(value: unknown): string {
	if (!value) return "";
	if (typeof value === "string") return "";
	if (Array.isArray(value)) {
		for (const item of value) {
			const code = desktopErrorCode(item);
			if (code) return code;
		}
		return "";
	}
	const typed = value as DesktopErrorShape;
	return typed.code || typed.Code || desktopErrorCode(typed.cause ?? typed.Cause);
}

export function desktopErrorDetails(value: unknown): Record<string, unknown> | null {
	if (!value || typeof value === "string") return null;
	if (Array.isArray(value)) {
		for (const item of value) {
			const details = desktopErrorDetails(item);
			if (details) return details;
		}
		return null;
	}
	const typed = value as DesktopErrorShape;
	return typed.details ?? typed.Details ?? desktopErrorDetails(typed.cause ?? typed.Cause);
}

export function desktopErrorMessage(value: unknown, fallback: string): string {
	if (!value) return "";
	if (typeof value === "string") {
		const text = value.trim();
		if (!text || text === "{}") return fallback;
		const code = /^([A-Z][A-Z0-9_]+)(?::|$)/.exec(text)?.[1];
		// Backend messages can contain implementation details; only structured codes cross the UI boundary.
		return code ? userErrorMessage(code, fallback) : fallback;
	}
	if (Array.isArray(value)) {
		return [...new Set(value.map((item) => desktopErrorMessage(item, fallback)).filter(Boolean))].join("\n");
	}
	const code = desktopErrorCode(value);
	return code ? userErrorMessage(code, fallback) : fallback;
}

export function isDesktopErrorCode(value: unknown, code: string): boolean {
	if (desktopErrorCode(value) === code) return true;
	if (typeof value === "string") return value.includes(code);
	if (Array.isArray(value)) return value.some((item) => isDesktopErrorCode(item, code));
	return false;
}

export function isCancelError(value: unknown): boolean {
	if (!value) return false;
	if (value instanceof CancelError) return true;
	if (Array.isArray(value)) return value.some(isCancelError);
	const typed = value as DesktopErrorShape;
	if (typed.name === "CancelError" || typed.code === "CANCELED" || typed.Code === "CANCELED") return true;
	const cause = typed.cause ?? typed.Cause;
	if (cause && cause !== value && isCancelError(cause)) return true;
	return typeof typed.message === "string" && typed.message.toLowerCase() === "context canceled";
}
