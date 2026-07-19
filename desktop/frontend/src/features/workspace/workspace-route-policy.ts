import type {
	AntigravityProfileRoute,
	ClaudeCodeProfileRoute,
	CodexProfileRoute,
} from "../profiles/types";

export type WorkspaceView =
	| "profiles"
	| "antigravity-profiles"
	| "claude-code-profiles"
	| "usage"
	| "codex-settings"
	| "settings"
	| "diagnostics";

export type AgentID = "codex" | "antigravity" | "claude-code";

export type WorkspaceRoute = {
	view: WorkspaceView;
	codexProfile: CodexProfileRoute;
	antigravityProfile: AntigravityProfileRoute;
	claudeCodeProfile: ClaudeCodeProfileRoute;
	valid: boolean;
};

const codexList: CodexProfileRoute = { kind: "list", profileID: "" };
const antigravityList: AntigravityProfileRoute = { kind: "list", profileID: "" };
const claudeCodeList: ClaudeCodeProfileRoute = { kind: "list", profileID: "" };

export function parseWorkspaceRoute(path: string): WorkspaceRoute {
	const build = (
		view: WorkspaceView,
		codexProfile: CodexProfileRoute = codexList,
		antigravityProfile: AntigravityProfileRoute = antigravityList,
		claudeCodeProfile: ClaudeCodeProfileRoute = claudeCodeList,
	): WorkspaceRoute => ({ view, codexProfile, antigravityProfile, claudeCodeProfile, valid: true });
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

	if (path === "/antigravity/profiles") return build("antigravity-profiles");
	if (path === "/antigravity/profiles/new") {
		return build("antigravity-profiles", codexList, { kind: "new", profileID: "" });
	}
	const antigravityDetail = path.match(/^\/antigravity\/profiles\/([^/]+)$/);
	if (antigravityDetail) {
		const profileID = decodeRouteID(antigravityDetail[1]);
		return profileID
			? build("antigravity-profiles", codexList, { kind: "detail", profileID })
			: { ...build("antigravity-profiles"), valid: false };
	}

	if (path === "/claude-code/profiles") return build("claude-code-profiles");
	if (path === "/claude-code/profiles/new") {
		return build("claude-code-profiles", codexList, antigravityList, { kind: "new", profileID: "" });
	}
	const claudeCodeDetail = path.match(/^\/claude-code\/profiles\/([^/]+)$/);
	if (claudeCodeDetail) {
		const profileID = decodeRouteID(claudeCodeDetail[1]);
		return profileID
			? build("claude-code-profiles", codexList, antigravityList, { kind: "detail", profileID })
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
		|| view === "usage"
		|| view === "codex-settings";
}

export function agentForWorkspace(view: WorkspaceView): AgentID | null {
	if (view === "antigravity-profiles") return "antigravity";
	if (view === "claude-code-profiles") return "claude-code";
	return view === "profiles" || view === "usage" || view === "codex-settings" ? "codex" : null;
}

export function agentHome(agentID: AgentID): string {
	if (agentID === "antigravity") return "/antigravity/profiles";
	if (agentID === "claude-code") return "/claude-code/profiles";
	return "/codex/profiles";
}
