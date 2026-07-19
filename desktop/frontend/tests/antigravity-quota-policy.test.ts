import { describe, expect, it } from "vitest";

import {
	AntigravityQuotaReadPolicy,
	AntigravityQuotaRequestGate,
	pruneAntigravityQuotaChecks,
	pruneAntigravityQuotaRecords,
} from "../src/features/profiles/antigravity-quota-policy.js";

describe("Antigravity quota policy", () => {
	it("reads the current Profile at most once during startup", () => {
		const policy = new AntigravityQuotaReadPolicy();
		expect(policy.startup("work")).toBe("work");
		expect(policy.startup("personal")).toBe("");
	});

	it("does not turn later navigation into a startup read", () => {
		const policy = new AntigravityQuotaReadPolicy();
		expect(policy.startup("")).toBe("");
		expect(policy.startup("work")).toBe("");
	});

	it("reads once after a successful Antigravity switch", () => {
		const policy = new AntigravityQuotaReadPolicy();
		const switched = {
			kind: "switch-applied",
			status: "success",
			provider_id: "antigravity",
			profile_id: "work",
			operation_id: "operation-1",
		};
		expect(policy.afterSwitch(switched)).toBe("work");
		expect(policy.afterSwitch(switched)).toBe("");
		expect(policy.afterSwitch({ ...switched, status: "failure", operation_id: "operation-2" })).toBe("");
		expect(policy.afterSwitch({ ...switched, provider_id: "codex", operation_id: "operation-3" })).toBe("");
		expect(policy.afterSwitch({ ...switched, kind: "antigravity-profile-changed", operation_id: "operation-4" })).toBe("");
	});

	it("deduplicates manual reads until the current request finishes", () => {
		const gate = new AntigravityQuotaRequestGate();
		expect(gate.start("work")).toBe(true);
		expect(gate.start("work")).toBe(false);
		expect(gate.start("personal")).toBe(true);
		gate.finish("work");
		expect(gate.start("work")).toBe(true);
		gate.clear();
		expect(gate.start("personal")).toBe(true);
	});

	it("prunes session cache when a Profile is deleted or its binding changes", () => {
		const profiles = [
			{ profile: { id: "keep" }, credential_id: "credential-a" },
			{ profile: { id: "changed" }, credential_id: "credential-new" },
		];
		const quotas = pruneAntigravityQuotaRecords(profiles, {
			keep: { credential_id: "credential-a", status: "available" },
			changed: { credential_id: "credential-old", status: "available" },
			deleted: { credential_id: "credential-deleted", status: "available" },
		});
		const checks = pruneAntigravityQuotaChecks(profiles, {
			keep: { credentialID: "credential-a", checkedAtUnixMS: 1 },
			changed: { credentialID: "credential-old", checkedAtUnixMS: 2 },
			deleted: { credentialID: "credential-deleted", checkedAtUnixMS: 3 },
		});
		expect(Object.keys(quotas)).toEqual(["keep"]);
		expect(Object.keys(checks)).toEqual(["keep"]);
	});
});
