import type { CodexConfigSet, CodexProfileQuota, CodexProfileSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

export type CodexProfileRoute =
	| { kind: "list"; profileID: "" }
	| { kind: "new"; profileID: "" }
	| { kind: "config-sets"; profileID: "" }
	| { kind: "detail"; profileID: string }
	| { kind: "fork"; profileID: string };

export type CodexProfileListItem = {
	summary: CodexProfileSummary;
	id: string;
	name: string;
	description: string;
	quota: CodexProfileQuota | null;
	quotaLoading: boolean;
	quotaCheckedAtUnixMS: number;
	quotaCheckOutcome: "updated" | "checked" | "failed" | "never";
};

export type ProfileUseRequest = {
	profileID: string;
	sequence: number;
};

export type AntigravityProfileRoute =
	| { kind: "list"; profileID: "" }
	| { kind: "new"; profileID: "" }
	| { kind: "detail"; profileID: string };

export type SwitchProfileItem = {
	id: string;
	name: string;
};

export type CodexForkBinding = "share-parent" | "copy-new";
export type ConfigSetDialogMode = "create" | "copy" | "edit";
export type ConfigSetDialogState = {
	mode: ConfigSetDialogMode;
	source: CodexConfigSet | null;
};
