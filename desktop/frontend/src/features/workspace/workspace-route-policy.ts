import type {
	AntigravityProfileRoute,
	ClaudeCodeProfileRoute,
	CodexProfileRoute,
	GrokBuildProfileRoute,
} from "../profiles/types";

export type WorkspaceView =
	| "profiles"
	| "grok-build-profiles"
	| "antigravity-profiles"
	| "claude-code-profiles"
	| "usage"
	| "codex-settings"
	| "settings"
	| "diagnostics";

export type AgentID = "codex" | "grok-build" | "antigravity" | "claude-code";

export type WorkspaceRoute = {
	view: WorkspaceView;
	codexProfile: CodexProfileRoute;
	grokBuildProfile: GrokBuildProfileRoute;
	antigravityProfile: AntigravityProfileRoute;
	claudeCodeProfile: ClaudeCodeProfileRoute;
	valid: boolean;
};

const codexList: CodexProfileRoute = { kind: "list", profileID: "" };
const grokBuildList: GrokBuildProfileRoute = { kind: "list", profileID: "" };
const antigravityList: AntigravityProfileRoute = { kind: "list", profileID: "" };
const claudeCodeList: ClaudeCodeProfileRoute = { kind: "list", profileID: "" };

export function parseWorkspaceRoute(path: string): WorkspaceRoute {
	const build = (
		view: WorkspaceView,
		codexProfile: CodexProfileRoute = codexList,
		grokBuildProfile: GrokBuildProfileRoute = grokBuildList,
		antigravityProfile: AntigravityProfileRoute = antigravityList,
		claudeCodeProfile: ClaudeCodeProfileRoute = claudeCodeList,
	): WorkspaceRoute => ({ view, codexProfile, grokBuildProfile, antigravityProfile, claudeCodeProfile, valid: true });
	const list = (): WorkspaceRoute => build("profiles");

	if (path === "/" || path === "/codex/profiles") return list();
	if (path === "/codex/config-sets") return build("profiles", { kind: "config-sets", profileID: "" });
	if (path === "/codex/profiles/new") return build("profiles", { kind: "new", profileID: "" });

	const fork = path.match(/^\/codex\/profiles\/([^/]+)\/fork$/);
	if (fork) {
		const profileID = decodeRouteID(fork[1]);
		return profileID ? build("profiles", { kind: "fork", profileID }) : { ...list(), valid: false };
	}
	const detail = path.match(/^\/codex\/profiles\/([^/]+)$/);
	if (detail) {
		const profileID = decodeRouteID(detail[1]);
		return profileID ? build("profiles", { kind: "detail", profileID }) : { ...list(), valid: false };
	}

	if (path === "/grok-build/profiles") return build("grok-build-profiles");
	if (path === "/grok-build/config-sets") {
		return build("grok-build-profiles", codexList, { kind: "config-sets", profileID: "" });
	}
	if (path === "/grok-build/profiles/new") {
		return build("grok-build-profiles", codexList, { kind: "new", profileID: "" });
	}
	const grokBuildFork = path.match(/^\/grok-build\/profiles\/([^/]+)\/fork$/);
	if (grokBuildFork) {
		const profileID = decodeRouteID(grokBuildFork[1]);
		return profileID
			? build("grok-build-profiles", codexList, { kind: "fork", profileID })
			: { ...build("grok-build-profiles"), valid: false };
	}
	const grokBuildDetail = path.match(/^\/grok-build\/profiles\/([^/]+)$/);
	if (grokBuildDetail) {
		const profileID = decodeRouteID(grokBuildDetail[1]);
		return profileID
			? build("grok-build-profiles", codexList, { kind: "detail", profileID })
			: { ...build("grok-build-profiles"), valid: false };
	}

	if (path === "/antigravity/profiles") return build("antigravity-profiles");
	if (path === "/antigravity/profiles/new") {
		return build("antigravity-profiles", codexList, grokBuildList, { kind: "new", profileID: "" });
	}
	const antigravityDetail = path.match(/^\/antigravity\/profiles\/([^/]+)$/);
	if (antigravityDetail) {
		const profileID = decodeRouteID(antigravityDetail[1]);
		return profileID
			? build("antigravity-profiles", codexList, grokBuildList, { kind: "detail", profileID })
			: { ...build("antigravity-profiles"), valid: false };
	}

	if (path === "/claude-code/profiles") return build("claude-code-profiles");
	if (path === "/claude-code/profiles/new") {
		return build("claude-code-profiles", codexList, grokBuildList, antigravityList, { kind: "new", profileID: "" });
	}
	const claudeCodeDetail = path.match(/^\/claude-code\/profiles\/([^/]+)$/);
	if (claudeCodeDetail) {
		const profileID = decodeRouteID(claudeCodeDetail[1]);
		return profileID
			? build("claude-code-profiles", codexList, grokBuildList, antigravityList, { kind: "detail", profileID })
			: { ...build("claude-code-profiles"), valid: false };
	}

	if (path === "/codex/usage") return build("usage");
	if (path === "/codex/settings") return build("codex-settings");
	if (path === "/settings") return build("settings");
	if (path === "/diagnostics" || path === "/codex/health") return build("diagnostics");
	return { ...list(), valid: false };
}

export function decodeRouteID(value: string): string | null {
	try {
		const decoded = decodeURIComponent(value);
		return decoded.length <= 80 && /^[a-z0-9][a-z0-9._-]*$/.test(decoded) ? decoded : null;
	} catch {
		return null;
	}
}

export function isAgentWorkspace(view: WorkspaceView): boolean {
	return view === "profiles"
		|| view === "antigravity-profiles"
		|| view === "claude-code-profiles"
		|| view === "grok-build-profiles"
		|| view === "usage"
		|| view === "codex-settings";
}

export function agentForWorkspace(view: WorkspaceView): AgentID | null {
	if (view === "antigravity-profiles") return "antigravity";
	if (view === "claude-code-profiles") return "claude-code";
	if (view === "grok-build-profiles") return "grok-build";
	return view === "profiles" || view === "usage" || view === "codex-settings" ? "codex" : null;
}

export function agentHome(agentID: AgentID): string {
	if (agentID === "antigravity") return "/antigravity/profiles";
	if (agentID === "claude-code") return "/claude-code/profiles";
	if (agentID === "grok-build") return "/grok-build/profiles";
	return "/codex/profiles";
}
