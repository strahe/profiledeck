import assert from "node:assert/strict";
import test from "node:test";

import {
	AntigravityQuotaReadPolicy,
	AntigravityQuotaRequestGate,
	pruneAntigravityQuotaChecks,
	pruneAntigravityQuotaRecords,
} from "../src/features/profiles/antigravity-quota-policy.js";

test("startup reads the current Antigravity Profile at most once", () => {
	const policy = new AntigravityQuotaReadPolicy();
	assert.equal(policy.startup("work"), "work");
	assert.equal(policy.startup("personal"), "");
});

test("startup without a current Profile does not turn later navigation into a read", () => {
	const policy = new AntigravityQuotaReadPolicy();
	assert.equal(policy.startup(""), "");
	assert.equal(policy.startup("work"), "");
});

test("only successful Antigravity switches trigger one read", () => {
	const policy = new AntigravityQuotaReadPolicy();
	const switched = {
		kind: "switch-applied",
		status: "success",
		provider_id: "antigravity",
		profile_id: "work",
		operation_id: "operation-1",
	};
	assert.equal(policy.afterSwitch(switched), "work");
	assert.equal(policy.afterSwitch(switched), "");
	assert.equal(policy.afterSwitch({ ...switched, status: "failure", operation_id: "operation-2" }), "");
	assert.equal(policy.afterSwitch({ ...switched, provider_id: "codex", operation_id: "operation-3" }), "");
	assert.equal(policy.afterSwitch({ ...switched, kind: "antigravity-profile-changed", operation_id: "operation-4" }), "");
});

test("manual reads are deduplicated until the current request finishes", () => {
	const gate = new AntigravityQuotaRequestGate();
	assert.equal(gate.start("work"), true);
	assert.equal(gate.start("work"), false);
	assert.equal(gate.start("personal"), true);
	gate.finish("work");
	assert.equal(gate.start("work"), true);
	gate.clear();
	assert.equal(gate.start("personal"), true);
});

test("session cache is pruned when a Profile is deleted or its binding changes", () => {
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
	assert.deepEqual(Object.keys(quotas), ["keep"]);
	assert.deepEqual(Object.keys(checks), ["keep"]);
});
