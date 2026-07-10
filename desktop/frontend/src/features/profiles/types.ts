import type { CodexProfileSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

export type CodexProfileRoute =
	| { kind: "list"; profileID: "" }
	| { kind: "new"; profileID: "" }
	| { kind: "detail"; profileID: string }
	| { kind: "fork"; profileID: string };

export type CodexProfileListItem = {
	summary: CodexProfileSummary;
	id: string;
	name: string;
	description: string;
	updated: string;
	model: string;
	provider: string;
	baseURL: string;
	account: string;
};

export type ProfileUseRequest = {
	profileID: string;
	sequence: number;
};

export type CodexForkAuthBinding = "share-parent" | "copy-new";
export type CodexSyncAuthUpdate = "update-shared" | "fork-new";
