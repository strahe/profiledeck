import { describe, expect, it } from "vitest";

import {
	agentForWorkspace,
	agentHome,
	isAgentWorkspace,
	parseWorkspaceRoute,
} from "../src/features/workspace/workspace-route-policy";

describe("Workspace route policy", () => {
	it.each([
		["/codex/profiles", "profiles"],
		["/antigravity/profiles", "antigravity-profiles"],
		["/claude-code/profiles", "claude-code-profiles"],
		["/codex/usage", "usage"],
		["/codex/settings", "codex-settings"],
		["/settings", "settings"],
		["/diagnostics", "diagnostics"],
	] as const)("maps %s to %s", (path, view) => {
		const route = parseWorkspaceRoute(path);
		expect(route.valid).toBe(true);
		expect(route.view).toBe(view);
	});

	it("parses create, detail, fork, and Config Set routes", () => {
		expect(parseWorkspaceRoute("/codex/profiles/new").codexProfile).toEqual({ kind: "new", profileID: "" });
		expect(parseWorkspaceRoute("/codex/profiles/work/fork").codexProfile).toEqual({ kind: "fork", profileID: "work" });
		expect(parseWorkspaceRoute("/codex/config-sets").codexProfile).toEqual({ kind: "config-sets", profileID: "" });
		expect(parseWorkspaceRoute("/antigravity/profiles/personal").antigravityProfile).toEqual({ kind: "detail", profileID: "personal" });
		expect(parseWorkspaceRoute("/claude-code/profiles/new").claudeCodeProfile).toEqual({ kind: "new", profileID: "" });
	});

	it("decodes a valid encoded Profile ID", () => {
		expect(parseWorkspaceRoute("/codex/profiles/profile%2Done").codexProfile).toEqual({
			kind: "detail",
			profileID: "profile-one",
		});
	});

	it.each([
		"/unknown",
		"/codex/profiles/%2F",
		"/codex/profiles/%E0%A4%A",
		`/codex/profiles/${"a".repeat(81)}`,
	])("rejects invalid path %s", (path) => {
		expect(parseWorkspaceRoute(path).valid).toBe(false);
	});

	it("maps every Agent workspace and home independently", () => {
		expect(agentForWorkspace("profiles")).toBe("codex");
		expect(agentForWorkspace("usage")).toBe("codex");
		expect(agentForWorkspace("codex-settings")).toBe("codex");
		expect(agentForWorkspace("antigravity-profiles")).toBe("antigravity");
		expect(agentForWorkspace("claude-code-profiles")).toBe("claude-code");
		expect(agentForWorkspace("settings")).toBeNull();
		expect(agentForWorkspace("diagnostics")).toBeNull();
		expect(agentHome("codex")).toBe("/codex/profiles");
		expect(agentHome("antigravity")).toBe("/antigravity/profiles");
		expect(agentHome("claude-code")).toBe("/claude-code/profiles");
		expect(isAgentWorkspace("usage")).toBe(true);
		expect(isAgentWorkspace("settings")).toBe(false);
	});
});
