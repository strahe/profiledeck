import assert from "node:assert/strict";
import test from "node:test";

import { CodexStartupQuotaReadCoordinator } from "../src/features/profiles/codex-quota-policy.js";

test("Codex startup waits for the initial runtime snapshot before reading quota", async () => {
	const coordinator = new CodexStartupQuotaReadCoordinator();
	let releaseRuntime;
	const runtimeReady = new Promise((resolve) => { releaseRuntime = resolve; });
	const reads = [];

	const started = coordinator.start("work", runtimeReady, (profileID) => reads.push(profileID));
	assert.deepEqual(reads, []);

	releaseRuntime();
	await started;
	assert.deepEqual(reads, ["work"]);
});

test("Codex startup reads the active Profile at most once", async () => {
	const coordinator = new CodexStartupQuotaReadCoordinator();
	const reads = [];

	await coordinator.start("work", Promise.resolve(), (profileID) => reads.push(profileID));
	await coordinator.start("personal", Promise.resolve(), (profileID) => reads.push(profileID));

	assert.deepEqual(reads, ["work"]);
});

test("Codex startup without an active Profile does not read one later", async () => {
	const coordinator = new CodexStartupQuotaReadCoordinator();
	const reads = [];

	await coordinator.start("", Promise.resolve(), (profileID) => reads.push(profileID));
	await coordinator.start("work", Promise.resolve(), (profileID) => reads.push(profileID));

	assert.deepEqual(reads, []);
});
