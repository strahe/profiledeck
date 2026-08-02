import { describe, expect, it } from "vitest";

import {
	GrokBuildQuotaReadPolicy,
	grokBuildQuotaBindingKey,
	pruneGrokBuildQuotaRecords,
	reconcileGrokBuildProfileSummaries,
} from "../src/features/profiles/grok-build-quota-policy.js";

describe("Grok Build quota policy", () => {
	it("reads the current Profile at most once during startup", () => {
		const policy = new GrokBuildQuotaReadPolicy();
		expect(policy.startup("work", true)).toBe("work");
		expect(policy.startup("personal", true)).toBe("");
	});

	it("does not turn later navigation into a startup read", () => {
		const policy = new GrokBuildQuotaReadPolicy();
		expect(policy.startup("", true)).toBe("");
		expect(policy.startup("work", true)).toBe("");
	});

	it("consumes startup without reading when Grok Build is disabled", () => {
		const policy = new GrokBuildQuotaReadPolicy();
		expect(policy.startup("work", false)).toBe("");
		expect(policy.startup("work", true)).toBe("");
	});

	it("reads once after a successful Grok Build switch", () => {
		const policy = new GrokBuildQuotaReadPolicy();
		const switched = {
			kind: "switch-applied",
			status: "success",
			provider_id: "grok-build",
			profile_id: "work",
			operation_id: "operation-1",
		};
		expect(policy.afterSwitch(switched, true)).toBe("work");
		expect(policy.afterSwitch(switched, true)).toBe("");
		expect(policy.afterSwitch({ ...switched, status: "failure", operation_id: "operation-2" }, true)).toBe("");
		expect(policy.afterSwitch({ ...switched, provider_id: "codex", operation_id: "operation-3" }, true)).toBe("");
	});

	it("consumes a successful switch without reading when Grok Build is disabled", () => {
		const policy = new GrokBuildQuotaReadPolicy();
		const switched = {
			kind: "switch-applied",
			status: "success",
			provider_id: "grok-build",
			profile_id: "work",
			operation_id: "operation-1",
		};
		expect(policy.afterSwitch(switched, false)).toBe("");
		expect(policy.afterSwitch(switched, true)).toBe("");
	});

	it("keeps shared binding snapshots and prunes unreferenced combinations", () => {
		const shared = grokBuildQuotaBindingKey({ credential_id: "credential-a", config_set_id: "config-a" });
		const removed = grokBuildQuotaBindingKey({ credential_id: "credential-b", config_set_id: "config-b" });
		const result = pruneGrokBuildQuotaRecords([
			{ credential_id: "credential-a", config_set_id: "config-a" },
			{ credential_id: "credential-a", config_set_id: "config-a" },
		], { [shared]: { status: "available" }, [removed]: { status: "available" } });
		expect(Object.keys(result)).toEqual([shared]);
	});

	it("preserves the last complete Profile list when a Dashboard section is missing", () => {
		const current = [
			{ profile: { id: "first" }, active: true },
			{ profile: { id: "second" }, active: false },
		];
		expect(reconcileGrokBuildProfileSummaries(current, undefined, "second")).toEqual([
			{ profile: { id: "first" }, active: false },
			{ profile: { id: "second" }, active: true },
		]);
		expect(reconcileGrokBuildProfileSummaries(current, [], "")).toEqual([]);
	});
});
