import { beforeEach, describe, expect, it, vi } from "vitest";

const backend = vi.hoisted(() => ({
	readProfileQuota: vi.fn(),
}));

vi.mock("../bindings/github.com/strahe/profiledeck/desktop/backend", () => ({
	GrokBuildService: { ReadProfileQuota: backend.readProfileQuota },
}));

import type { ProfileSummary } from "../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
import { GrokBuildQuotaController } from "../src/features/profiles/grok-build-quota.svelte.js";

function summary(id = "work", active = true, credentialID = "credential-work", configSetID = "config-work"): ProfileSummary {
	return {
		profile: {
			id,
			name: id,
			description: "",
			metadata: {},
			created_at_unix_ms: 1,
			updated_at_unix_ms: 1,
		},
		provider_id: "grok-build",
		credential_id: credentialID,
		credential_reference_count: 1,
		config_set_id: configSetID,
		config_set_name: configSetID,
		config_set_reference_count: 1,
		active,
		updated_at_unix_ms: 1,
	};
}

function deferredCancellable<T>(rejectOnCancel = true) {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((done, fail) => {
		resolve = done;
		reject = fail;
	}) as Promise<T> & { cancel: (reason: string) => void };
	const cancel = vi.fn((reason: string) => {
		if (rejectOnCancel) reject({ code: "CANCELED", message: reason });
	});
	promise.cancel = cancel;
	return { promise, resolve, reject, cancel };
}

describe("GrokBuildQuotaController", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("never asks the backend to refresh an inactive Profile", async () => {
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		controller.setProfiles([summary("inactive", false)]);
		await expect(controller.readQuota("inactive")).resolves.toBeNull();
		expect(backend.readProfileQuota).not.toHaveBeenCalled();
	});

	it("reads the current Profile even while its Dashboard summary is unavailable", async () => {
		const result = {
			profile_id: "work",
			credential_id: "credential-work",
			config_set_id: "config-work",
			status: "available",
			snapshot: { fetched_at_unix_ms: 100, remaining_percent: 80 },
		};
		backend.readProfileQuota.mockReturnValueOnce(Promise.resolve(result));
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		controller.setProfiles([], "work");

		await expect(controller.readCurrentQuota("work")).resolves.toEqual(result);
		expect(backend.readProfileQuota).toHaveBeenCalledWith("work");

		const current = summary();
		controller.setProfiles([current], "work");
		expect(controller.quotaForSummary(current)).toMatchObject({
			status: "available",
			snapshot: { remaining_percent: 80 },
		});
	});

	it("shares an in-session snapshot only across the same login and Config Set", async () => {
		backend.readProfileQuota.mockReturnValueOnce(Promise.resolve({
			profile_id: "first",
			credential_id: "credential-shared",
			config_set_id: "config-shared",
			status: "available",
			snapshot: { fetched_at_unix_ms: 100, remaining_percent: 80 },
		}));
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		const first = summary("first", true, "credential-shared", "config-shared");
		const second = summary("second", false, "credential-shared", "config-shared");
		const otherConfig = summary("other", false, "credential-shared", "config-other");
		controller.setProfiles([first, second, otherConfig]);
		await controller.readQuota("first");

		expect(controller.quotaForSummary(second)).toMatchObject({
			profile_id: "second",
			status: "inactive",
			snapshot: { remaining_percent: 80 },
		});
		expect(controller.quotaForSummary(otherConfig)).toBeNull();
	});

	it("shares a pending read and preserves the last snapshot after failure", async () => {
		let resolveRequest!: (value: unknown) => void;
		const pending = new Promise((resolve) => { resolveRequest = resolve; });
		backend.readProfileQuota.mockReturnValueOnce(pending);
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		const active = summary();
		controller.setProfiles([active]);
		controller.quotaByBindingKey = {
			["credential-work\u0000config-work"]: {
				profile_id: "work",
				credential_id: "credential-work",
				config_set_id: "config-work",
				status: "available",
				snapshot: { fetched_at_unix_ms: 50, remaining_percent: 90 },
			},
		};

		const first = controller.readQuota("work");
		const duplicate = controller.readQuota("work");
		expect(backend.readProfileQuota).toHaveBeenCalledTimes(1);
		const result = {
			profile_id: "work",
			credential_id: "credential-work",
			config_set_id: "config-work",
			status: "unavailable",
		};
		resolveRequest(result);
		await expect(Promise.all([first, duplicate])).resolves.toEqual([result, result]);
		expect(controller.quotaForSummary(active)).toMatchObject({
			status: "unavailable",
			snapshot: { remaining_percent: 90 },
		});
	});

	it("cancels a pending read when the Profile binding changes", async () => {
		let rejectRequest!: (reason: unknown) => void;
		const pending = new Promise<never>((_resolve, reject) => { rejectRequest = reject; }) as Promise<never> & { cancel: (reason: string) => void };
		const cancel = vi.fn((reason: string) => rejectRequest({ code: "CANCELED", message: reason }));
		pending.cancel = cancel;
		backend.readProfileQuota.mockReturnValueOnce(pending);
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		controller.setProfiles([summary()]);
		const read = controller.readQuota("work");
		controller.setProfiles([summary("work", true, "credential-new", "config-work")]);
		expect(cancel).toHaveBeenCalledWith("profile-no-longer-active");
		await expect(read).resolves.toBeNull();
		expect(controller.isLoading("work")).toBe(false);
	});

	it("runs the new active Profile after canceling a stale read", async () => {
		const first = deferredCancellable<unknown>();
		const second = deferredCancellable<unknown>();
		backend.readProfileQuota
			.mockReturnValueOnce(first.promise)
			.mockReturnValueOnce(second.promise);
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		controller.setProfiles([
			summary("first", true, "credential-first", "config-first"),
			summary("second", false, "credential-second", "config-second"),
		]);

		const firstRead = controller.readQuota("first");
		controller.setProfiles([
			summary("first", false, "credential-first", "config-first"),
			summary("second", true, "credential-second", "config-second"),
		]);
		const secondRead = controller.readQuota("second");

		expect(first.cancel).toHaveBeenCalledWith("profile-no-longer-active");
		expect(backend.readProfileQuota).toHaveBeenCalledTimes(1);
		await expect(firstRead).resolves.toBeNull();
		expect(backend.readProfileQuota).toHaveBeenCalledTimes(2);
		const result = {
			profile_id: "second",
			credential_id: "credential-second",
			config_set_id: "config-second",
			status: "available",
			snapshot: { fetched_at_unix_ms: 200, remaining_percent: 70 },
		};
		second.resolve(result);
		await expect(secondRead).resolves.toEqual(result);
		expect(controller.quotaForSummary(summary("second", true, "credential-second", "config-second"))).toMatchObject({
			status: "available",
			snapshot: { remaining_percent: 70 },
		});
	});

	it("does not commit a late inactive result to a shared binding", async () => {
		const first = deferredCancellable<unknown>(false);
		const second = deferredCancellable<unknown>();
		backend.readProfileQuota
			.mockReturnValueOnce(first.promise)
			.mockReturnValueOnce(second.promise);
		const controller = new GrokBuildQuotaController({ showError: vi.fn() });
		const firstSummary = summary("first", true, "credential-shared", "config-shared");
		const secondSummary = summary("second", false, "credential-shared", "config-shared");
		controller.setProfiles([firstSummary, secondSummary]);
		controller.quotaByBindingKey = {
			["credential-shared\u0000config-shared"]: {
				profile_id: "first",
				credential_id: "credential-shared",
				config_set_id: "config-shared",
				status: "available",
				snapshot: { fetched_at_unix_ms: 50, remaining_percent: 90 },
			},
		};

		const firstRead = controller.readQuota("first");
		const activeSecond = { ...secondSummary, active: true };
		controller.setProfiles([{ ...firstSummary, active: false }, activeSecond]);
		const secondRead = controller.readQuota("second");
		first.resolve({
			profile_id: "first",
			credential_id: "credential-shared",
			config_set_id: "config-shared",
			status: "inactive",
		});
		await firstRead;

		expect(controller.quotaForSummary(activeSecond)).toMatchObject({
			status: "available",
			snapshot: { remaining_percent: 90 },
		});
		const result = {
			profile_id: "second",
			credential_id: "credential-shared",
			config_set_id: "config-shared",
			status: "available",
			snapshot: { fetched_at_unix_ms: 200, remaining_percent: 70 },
		};
		second.resolve(result);
		await expect(secondRead).resolves.toEqual(result);
		expect(controller.quotaForSummary(activeSecond)).toMatchObject({
			status: "available",
			snapshot: { remaining_percent: 70 },
		});
	});
});
