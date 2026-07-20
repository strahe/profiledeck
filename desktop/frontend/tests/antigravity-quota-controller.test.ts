import { describe, expect, it, vi } from "vitest";

const backend = vi.hoisted(() => ({
	readProfileQuota: vi.fn(),
}));

vi.mock("../bindings/github.com/strahe/profiledeck/desktop/backend", () => ({
	AntigravityService: { ReadProfileQuota: backend.readProfileQuota },
}));

import type { AntigravityProfileSummary } from "../bindings/github.com/strahe/profiledeck/internal/antigravity/models";
import { AntigravityQuotaController } from "../src/features/profiles/antigravity-quota.svelte.js";

function summary(): AntigravityProfileSummary {
	return {
		profile: {
			id: "work",
			name: "Work",
			description: "",
			metadata: {},
			created_at_unix_ms: 1,
			updated_at_unix_ms: 1,
		},
		provider_id: "antigravity",
		credential_id: "credential-work",
		credential_reference_count: 1,
		active: true,
		updated_at_unix_ms: 1,
	};
}

describe("AntigravityQuotaController", () => {
	it("cancels and clears late quota state when a deleted Profile disappears", async () => {
		let rejectRequest!: (reason: unknown) => void;
		const pending = new Promise<never>((_resolve, reject) => {
			rejectRequest = reject;
		}) as Promise<never> & { cancel: (reason: string) => void };
		const cancel = vi.fn((reason: string) => rejectRequest({ code: "CANCELED", message: reason }));
		pending.cancel = cancel;
		backend.readProfileQuota.mockReturnValueOnce(pending);
		const showError = vi.fn();
		const controller = new AntigravityQuotaController({ showError });
		controller.setProfiles([summary()]);
		controller.quotaByProfileID = {
			work: { profile_id: "work", credential_id: "credential-work", status: "available" },
		};
		controller.checkByProfileID = {
			work: { profileID: "work", credentialID: "credential-work", checkedAtUnixMS: 1, outcome: "updated" },
		};

		const read = controller.readQuota("work");
		expect(controller.isLoading("work")).toBe(true);
		controller.setProfiles([]);

		expect(cancel).toHaveBeenCalledWith("profile-binding-changed");
		expect(controller.isLoading("work")).toBe(false);
		expect(controller.quotaByProfileID).toEqual({});
		expect(controller.checkByProfileID).toEqual({});
		await expect(read).resolves.toBeNull();
		expect(showError).not.toHaveBeenCalled();
	});
});
