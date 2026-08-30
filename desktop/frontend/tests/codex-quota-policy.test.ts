import { describe, expect, it } from "vitest";

import { CodexStartupQuotaReadCoordinator, codexQuotaMatchesSummary } from "../src/features/profiles/codex-quota-policy.js";

describe("Codex startup quota policy", () => {
	it("waits for the initial runtime snapshot before reading quota", async () => {
		const coordinator = new CodexStartupQuotaReadCoordinator();
		let releaseRuntime!: () => void;
		const runtimeReady = new Promise<void>((resolve) => { releaseRuntime = resolve; });
		const reads: string[] = [];

		const started = coordinator.start("work", runtimeReady, (profileID) => reads.push(profileID));
		expect(reads).toEqual([]);

		releaseRuntime();
		await started;
		expect(reads).toEqual(["work"]);
	});

	it("reads the active Profile at most once during startup", async () => {
		const coordinator = new CodexStartupQuotaReadCoordinator();
		const reads: string[] = [];

		await coordinator.start("work", Promise.resolve(), (profileID) => reads.push(profileID));
		await coordinator.start("personal", Promise.resolve(), (profileID) => reads.push(profileID));

		expect(reads).toEqual(["work"]);
	});

	it("does not read a Profile discovered after startup", async () => {
		const coordinator = new CodexStartupQuotaReadCoordinator();
		const reads: string[] = [];

		await coordinator.start("", Promise.resolve(), (profileID) => reads.push(profileID));
		await coordinator.start("work", Promise.resolve(), (profileID) => reads.push(profileID));

		expect(reads).toEqual([]);
	});

	it("reads once after a successful Codex switch", () => {
		const coordinator = new CodexStartupQuotaReadCoordinator();
		const switched = {
			kind: "switch-applied",
			status: "success",
			provider_id: "codex",
			profile_id: "work",
			operation_id: "operation-1",
		};
		expect(coordinator.afterSwitch(switched)).toBe("work");
		expect(coordinator.afterSwitch(switched)).toBe("");
		expect(coordinator.afterSwitch({ ...switched, status: "failure", operation_id: "operation-2" })).toBe("");
		expect(coordinator.afterSwitch({ ...switched, provider_id: "grok-build", operation_id: "operation-3" })).toBe("");
	});

	it("matches API service quota to both credential and Config Set", () => {
		const summary = { credential_id: "credential", config_set_id: "config-a" };
		expect(codexQuotaMatchesSummary(summary, {
			credential_id: "credential", config_set_id: "config-a", source: "sub2api",
		})).toBe(true);
		expect(codexQuotaMatchesSummary(summary, {
			credential_id: "credential", config_set_id: "config-b", source: "sub2api",
		})).toBe(false);
		expect(codexQuotaMatchesSummary(summary, {
			credential_id: "credential", config_set_id: "config-b", source: "chatgpt",
		})).toBe(true);
	});
});
