import { describe, expect, it } from "vitest";

import { selectLatestUpdateStatus } from "../src/features/workspace/update-status-policy.js";

describe("update status policy", () => {
	it("does not replace a newer terminal event with a stale snapshot", () => {
		const ready = { revision: 4, state: "ready" };
		const staleChecking = { revision: 3, state: "checking" };

		expect(selectLatestUpdateStatus(ready, staleChecking)).toBe(ready);
	});

	it("allows a newer revision to start the next update check", () => {
		const upToDate = { revision: 4, state: "up_to_date" };
		const nextChecking = { revision: 5, state: "checking" };

		expect(selectLatestUpdateStatus(upToDate, nextChecking)).toBe(nextChecking);
	});
});
