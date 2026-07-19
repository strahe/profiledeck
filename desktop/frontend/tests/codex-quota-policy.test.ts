import { describe, expect, it } from "vitest";

import { CodexStartupQuotaReadCoordinator } from "../src/features/profiles/codex-quota-policy.js";

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
});
