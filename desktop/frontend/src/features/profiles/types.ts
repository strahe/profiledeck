import type { CodexConfigSet, CodexProfileQuota, CodexProfileSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/codex/models";
import type { Profile } from "../../../bindings/github.com/strahe/profiledeck/internal/profile/models";

export type CodexProfileRoute =
	| { kind: "list"; profileID: "" }
	| { kind: "new"; profileID: "" }
	| { kind: "config-sets"; profileID: "" }
	| { kind: "detail"; profileID: string }
	| { kind: "fork"; profileID: string };

export type GrokBuildProfileRoute = CodexProfileRoute;

export type ManagedProfileSummary = {
	profile: Profile;
	provider_id: string;
	credential_id?: string;
	credential_reference_count: number;
	config_set_id?: string;
	config_set_name?: string;
	config_set_reference_count: number;
	active: boolean;
	updated_at_unix_ms: number;
	warnings?: string[] | null;
};

export type ManagedConfigSet = {
	id: string;
	name: string;
	description?: string;
	reference_count: number;
	active: boolean;
	model?: string;
	model_provider?: string;
	created_at_unix_ms: number;
	updated_at_unix_ms: number;
};

export type ManagedProfileDetail = {
	summary: ManagedProfileSummary;
	login?: {
		credential_id: string;
		codex_account_id?: string;
		reference_count: number;
		updated_at_unix_ms: number;
	} | null;
	config_set?: ManagedConfigSet | null;
};

export type ManagedDetectResult = {
	config_status: string;
	auth_status: string;
	profiledeck_initialized: boolean;
	provider_compatible: boolean;
	file_auth_supported?: boolean;
	warnings?: string[] | null;
};

export type ManagedProfileListItem = {
	summary: ManagedProfileSummary;
	id: string;
	name: string;
	description: string;
	quota?: CodexProfileQuota | null;
	quotaLoading?: boolean;
	quotaCheckedAtUnixMS?: number;
	quotaCheckOutcome?: "updated" | "checked" | "failed" | "never";
};

export type CodexProfileListItem = ManagedProfileListItem & { summary: CodexProfileSummary };

export type ProfileUseRequest = {
	profileID: string;
	sequence: number;
};

export type AntigravityProfileRoute =
	| { kind: "list"; profileID: "" }
	| { kind: "new"; profileID: "" }
	| { kind: "detail"; profileID: string };

export type ClaudeCodeProfileRoute =
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
	source: ManagedConfigSet | null;
};
