import assert from "node:assert/strict";
import test from "node:test";

import { selectLatestUpdateStatus } from "../src/features/workspace/update-status-policy.js";

test("a stale update snapshot cannot replace a newer terminal event", () => {
	const ready = { revision: 4, state: "ready" };
	const staleChecking = { revision: 3, state: "checking" };

	assert.equal(selectLatestUpdateStatus(ready, staleChecking), ready);
});

test("a newer revision can start the next update check", () => {
	const upToDate = { revision: 4, state: "up_to_date" };
	const nextChecking = { revision: 5, state: "checking" };

	assert.equal(selectLatestUpdateStatus(upToDate, nextChecking), nextChecking);
});
