import { CancelError } from "@wailsio/runtime";

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
	if (typeof value === "string") return value.trim() === "{}" ? fallback : value;
	if (Array.isArray(value)) return value.map((item) => desktopErrorMessage(item, fallback)).filter(Boolean).join("\n");
	const typed = value as DesktopErrorShape;
	const cause = typed.cause ?? typed.Cause;
	if (cause && cause !== value) {
		const causeMessage = desktopErrorMessage(cause, fallback);
		if (causeMessage) return causeMessage;
	}
	const code = typed.code || typed.Code || "";
	const message = typed.message || typed.Message || "";
	if (code && message) return `${code}: ${message}`;
	if (message) return message;
	try {
		const json = JSON.stringify(value);
		return json === "{}" ? fallback : json;
	} catch {
		return String(value);
	}
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
