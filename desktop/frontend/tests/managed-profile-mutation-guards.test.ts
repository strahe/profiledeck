import { render, screen, waitFor, within } from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";
import { locale } from "svelte-i18n";
import { tick } from "svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
	CodexDetectResult,
	CodexProfileDetail,
} from "../bindings/github.com/strahe/profiledeck/internal/codex/models";
import type {
	DetectResult as GrokBuildDetectResult,
	ProfileDetail as GrokBuildProfileDetail,
	ProfileSummary as GrokBuildProfileSummary,
} from "../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
import type { Profile } from "../bindings/github.com/strahe/profiledeck/internal/profile/models";

const backend = vi.hoisted(() => ({
	createCodexProfile: vi.fn(),
	createGrokBuildProfile: vi.fn(),
	forkCodexProfile: vi.fn(),
	forkGrokBuildProfile: vi.fn(),
	saveCodexProfile: vi.fn(),
	saveGrokBuildProfile: vi.fn(),
	listCodexConfigSets: vi.fn(),
	listGrokBuildConfigSets: vi.fn(),
	showCodexProfile: vi.fn(),
	showGrokBuildProfile: vi.fn(),
	listProfiles: vi.fn(),
	deleteProfile: vi.fn(),
}));

vi.mock("../bindings/github.com/strahe/profiledeck/desktop/backend", () => ({
	CodexService: {
		CreateProfile: backend.createCodexProfile,
		ForkProfile: backend.forkCodexProfile,
		ShowProfile: backend.showCodexProfile,
		SaveActiveProfileState: backend.saveCodexProfile,
		ListConfigSets: backend.listCodexConfigSets,
	},
	GrokBuildService: {
		CreateProfile: backend.createGrokBuildProfile,
		ForkProfile: backend.forkGrokBuildProfile,
		SaveActiveProfileState: backend.saveGrokBuildProfile,
		ListConfigSets: backend.listGrokBuildConfigSets,
		ShowProfile: backend.showGrokBuildProfile,
	},
	ProfileService: {
		Delete: backend.deleteProfile,
		ListProfiles: backend.listProfiles,
	},
	SwitchService: {},
}));

import CodexProfiles from "../src/features/profiles/CodexProfiles.svelte";
import GrokBuildProfiles from "../src/features/profiles/GrokBuildProfiles.svelte";
import ProfileTestProviders from "./ProfileTestProviders.svelte";

const validGrokBuildDetect: GrokBuildDetectResult = {
	provider_id: "grok-build",
	adapter_id: "grok-build",
	grok_home: "/tmp/grok",
	config_path: "/tmp/grok/config.toml",
	auth_path: "/tmp/grok/auth.json",
	grok_home_exists: true,
	config_status: "valid",
	auth_status: "valid",
	file_auth_supported: true,
	profiledeck_initialized: true,
	provider_exists: true,
	provider_adapter_id: "grok-build",
	provider_compatible: true,
	warnings: [],
};

const validCodexDetect: CodexDetectResult = {
	provider_id: "codex",
	adapter_id: "codex",
	codex_dir: "/tmp/codex",
	config_path: "/tmp/codex/config.toml",
	auth_path: "/tmp/codex/auth.json",
	codex_dir_exists: true,
	config_status: "valid",
	auth_status: "valid",
	profiledeck_initialized: true,
	provider_exists: true,
	provider_adapter_id: "codex",
	provider_compatible: true,
	warnings: [],
};

const destinationProfile: Profile = {
	id: "shared-profile",
	name: "Shared Profile",
	description: "Keep this metadata",
	metadata: {},
	created_at_unix_ms: 1,
	updated_at_unix_ms: 1,
};

const codexDetail: CodexProfileDetail = {
	summary: {
		profile: {
			id: "work",
			name: "Work",
			description: "Source description",
			metadata: {},
			created_at_unix_ms: 1,
			updated_at_unix_ms: 1,
		},
		provider_id: "codex",
		credential_id: "credential",
		credential_reference_count: 1,
		config_set_id: "shared",
		config_set_name: "Shared",
		config_set_reference_count: 2,
		active: true,
		updated_at_unix_ms: 1,
		warnings: [],
	},
	login: {
		credential_id: "credential",
		reference_count: 1,
		updated_at_unix_ms: 1,
	},
	config_set: {
		id: "shared",
		name: "Shared",
		description: "",
		reference_count: 2,
		active: true,
		payload_sha256: "sha256",
		created_at_unix_ms: 1,
		updated_at_unix_ms: 1,
	},
};

const grokBuildDetail: GrokBuildProfileDetail = {
	summary: {
		profile: {
			id: "work",
			name: "Work",
			description: "",
			metadata: {},
			created_at_unix_ms: 1,
			updated_at_unix_ms: 1,
		},
		provider_id: "grok-build",
		credential_id: "credential",
		credential_reference_count: 1,
		config_set_id: "shared",
		config_set_name: "Shared",
		config_set_reference_count: 2,
		active: true,
		updated_at_unix_ms: 1,
		warnings: [],
	},
	login: {
		credential_id: "credential",
		reference_count: 1,
		updated_at_unix_ms: 1,
	},
	config_set: {
		id: "shared",
		name: "Shared",
		description: "",
		reference_count: 2,
		active: true,
		created_at_unix_ms: 1,
		updated_at_unix_ms: 1,
	},
};

function cancellableResolved<T>(value: T): Promise<T> & { cancel: () => void } {
	const promise = Promise.resolve(value) as Promise<T> & { cancel: () => void };
	promise.cancel = vi.fn();
	return promise;
}

function cancellableRejected(error: unknown): Promise<never> & { cancel: () => void } {
	const promise = Promise.reject(error) as Promise<never> & { cancel: () => void };
	promise.cancel = vi.fn();
	return promise;
}

function grokBuildQuotaProps() {
	return {
		quotaForSummary: (_summary: GrokBuildProfileSummary) => null,
		quotaCheckForSummary: (summary: GrokBuildProfileSummary) => ({
			profileID: summary.profile.id,
			credentialID: summary.credential_id ?? "",
			configSetID: summary.config_set_id ?? "",
			checkedAtUnixMS: 0,
			outcome: "never" as const,
		}),
		quotaLoading: (_profileID: string) => false,
		refreshQuota: vi.fn().mockResolvedValue(null),
	};
}

beforeEach(async () => {
	vi.clearAllMocks();
	backend.listCodexConfigSets.mockImplementation(() => cancellableResolved({ config_sets: [] }));
	backend.listGrokBuildConfigSets.mockImplementation(() => cancellableResolved({ config_sets: [] }));
	locale.set("en");
	await tick();
});

describe("managed Profile mutation guards", () => {
	it("saves the active Codex Profile from its list and shows shared settings", async () => {
		const user = userEvent.setup();
		backend.showCodexProfile.mockReturnValue(cancellableResolved(codexDetail));
		backend.saveCodexProfile.mockImplementation(() => cancellableResolved({ warnings: [] }));
		render(CodexProfiles, {
			route: { kind: "list", profileID: "" },
			profiles: [{ ...codexDetail.summary, config_set_reference_count: 1 }],
			dashboardConfigSets: [],
			detectResult: validCodexDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validCodexDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });
		await user.click(screen.getAllByRole("button", { name: "More actions" }).at(-1)!);
		await user.click(await screen.findByText("Save Current Login and Settings"));
		const dialog = await screen.findByRole("alertdialog");
		expect(within(dialog).getByText("These settings are used by 2 Profiles. Saving them will change them for all of them.")).toBeInTheDocument();
		expect(backend.showCodexProfile).toHaveBeenCalledWith("work");
		await user.click(within(dialog).getByRole("button", { name: "Save Current Login and Settings" }));
		await waitFor(() => expect(backend.saveCodexProfile).toHaveBeenCalledWith("work", 1, 2));
	});

	it("shows Grok Build shared settings from the active list row", async () => {
		const user = userEvent.setup();
		backend.showGrokBuildProfile.mockReturnValue(cancellableResolved(grokBuildDetail));
		render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "list", profileID: "" },
			profiles: [{ ...grokBuildDetail.summary, config_set_reference_count: 1 }],
			dashboardConfigSets: [],
			detectResult: validGrokBuildDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validGrokBuildDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });
		await user.click(screen.getAllByRole("button", { name: "More actions" }).at(-1)!);
		await user.click(await screen.findByText("Save Current Login and Settings"));
		expect(within(await screen.findByRole("alertdialog")).getByText("These settings are used by 2 Profiles. Saving them will change them for all of them.")).toBeInTheDocument();
		expect(backend.showGrokBuildProfile).toHaveBeenCalledWith("work");
	});

	it("refreshes sharing before confirming from a Codex detail page", async () => {
		const user = userEvent.setup();
		const staleDetail = { ...codexDetail, summary: { ...codexDetail.summary, config_set_reference_count: 1 } };
		backend.showCodexProfile
			.mockReturnValueOnce(cancellableResolved(staleDetail))
			.mockReturnValueOnce(cancellableResolved(codexDetail));
		render(CodexProfiles, {
			route: { kind: "detail", profileID: "work" },
			profiles: [staleDetail.summary],
			dashboardConfigSets: [],
			detectResult: validCodexDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validCodexDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });
		await screen.findByRole("heading", { name: "Work" });
		await user.click(screen.getByRole("button", { name: "More actions" }));
		await user.click(await screen.findByText("Save Current Login and Settings"));
		expect(within(await screen.findByRole("alertdialog")).getByText("These settings are used by 2 Profiles. Saving them will change them for all of them.")).toBeInTheDocument();
		expect(backend.showCodexProfile).toHaveBeenCalledTimes(2);
	});

	it("does not open the save confirmation when current sharing cannot be read", async () => {
		const user = userEvent.setup();
		const showError = vi.fn();
		backend.showCodexProfile.mockImplementation(() => cancellableRejected({ code: "STORE_STATUS_FAILED" }));
		render(CodexProfiles, {
			route: { kind: "list", profileID: "" },
			profiles: [codexDetail.summary],
			dashboardConfigSets: [],
			detectResult: validCodexDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validCodexDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError,
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });
		await user.click(screen.getAllByRole("button", { name: "More actions" }).at(-1)!);
		await user.click(await screen.findByText("Save Current Login and Settings"));
		await waitFor(() => expect(showError).toHaveBeenCalledWith({ code: "STORE_STATUS_FAILED" }));
		expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
		expect(backend.saveCodexProfile).not.toHaveBeenCalled();
	});

	it("closes the save confirmation when sharing changes before the commit", async () => {
		const user = userEvent.setup();
		const showError = vi.fn();
		const refreshProfiles = vi.fn().mockResolvedValue(undefined);
		backend.showGrokBuildProfile.mockImplementation(() => cancellableResolved(grokBuildDetail));
		backend.saveGrokBuildProfile.mockImplementation(() => cancellableRejected({ code: "PROFILE_SHARING_CHANGED" }));
		render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "list", profileID: "" },
			profiles: [grokBuildDetail.summary],
			dashboardConfigSets: [],
			detectResult: validGrokBuildDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validGrokBuildDetect),
			refreshProfiles,
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError,
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });
		await user.click(screen.getAllByRole("button", { name: "More actions" }).at(-1)!);
		await user.click(await screen.findByText("Save Current Login and Settings"));
		const dialog = await screen.findByRole("alertdialog");
		await user.click(within(dialog).getByRole("button", { name: "Save Current Login and Settings" }));
		await waitFor(() => expect(showError).toHaveBeenCalledWith({ code: "PROFILE_SHARING_CHANGED" }));
		expect(refreshProfiles).toHaveBeenCalled();
		expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
		expect(backend.saveGrokBuildProfile).toHaveBeenCalledWith("work", 1, 2);
	});

	it("shows Grok Build create recheck failures and does not create a Profile", async () => {
		const user = userEvent.setup();
		const invalid = { ...validGrokBuildDetect, auth_status: "invalid" };
		const refreshDetect = vi.fn()
			.mockResolvedValueOnce(validGrokBuildDetect)
			.mockResolvedValueOnce(invalid)
			.mockResolvedValueOnce(validGrokBuildDetect);

		const view = render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "new", profileID: "" },
			profiles: [],
			dashboardConfigSets: [],
			detectResult: validGrokBuildDetect,
			detectError: "",
			activeProfileID: "active",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect,
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		await waitFor(() => expect(refreshDetect).toHaveBeenCalledOnce());
		await user.type(screen.getByLabelText("Profile ID"), "work");
		await user.click(screen.getByRole("button", { name: "Save Current Grok Build as a New Profile" }));

		const message = "Grok Build files are not ready: config.toml is Valid; auth.json is Invalid.";
		expect(await screen.findByText(message)).toBeInTheDocument();
		expect(backend.createGrokBuildProfile).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(screen.queryByText(message)).not.toBeInTheDocument());

		refreshDetect.mockResolvedValueOnce(invalid);
		await user.click(screen.getByRole("button", { name: "Save Current Grok Build as a New Profile" }));
		expect(await screen.findByText(message)).toBeInTheDocument();

		await view.rerender({ detectResult: invalid });
		await view.rerender({ detectResult: validGrokBuildDetect });
		await waitFor(() => expect(screen.queryByText(message)).not.toBeInTheDocument());
		expect(backend.createGrokBuildProfile).not.toHaveBeenCalled();
	});

	it("shows Codex create recheck failures and does not create a Profile", async () => {
		const user = userEvent.setup();
		const refreshDetect = vi.fn()
			.mockResolvedValueOnce(validCodexDetect)
			.mockResolvedValueOnce(null)
			.mockResolvedValueOnce(validCodexDetect);

		const view = render(CodexProfiles, {
			route: { kind: "new", profileID: "" },
			profiles: [],
			dashboardConfigSets: [],
			detectResult: validCodexDetect,
			detectError: "",
			activeProfileID: "active",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect,
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		await waitFor(() => expect(refreshDetect).toHaveBeenCalledOnce());
		await user.type(screen.getByLabelText("Profile ID"), "work");
		await user.click(screen.getByRole("button", { name: "Save Current Codex as a New Profile" }));

		const message = "ProfileDeck could not check the current Codex files. Retry before saving this Profile.";
		expect(await screen.findByText(message)).toBeInTheDocument();
		expect(backend.createCodexProfile).not.toHaveBeenCalled();

		await user.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(screen.queryByText(message)).not.toBeInTheDocument());

		refreshDetect.mockResolvedValueOnce(null);
		await user.click(screen.getByRole("button", { name: "Save Current Codex as a New Profile" }));
		expect(await screen.findByText(message)).toBeInTheDocument();

		await view.rerender({ detectResult: null });
		await view.rerender({ detectResult: validCodexDetect });
		await waitFor(() => expect(screen.queryByText(message)).not.toBeInTheDocument());
		expect(backend.createCodexProfile).not.toHaveBeenCalled();
	});

	it("preserves existing destination metadata when Codex Fork defaults are untouched", async () => {
		const user = userEvent.setup();
		backend.showCodexProfile.mockReturnValue(cancellableResolved(codexDetail));
		backend.listProfiles.mockReturnValue(cancellableResolved([destinationProfile]));
		backend.forkCodexProfile.mockReturnValue(cancellableResolved({
			profile: destinationProfile,
			warnings: [],
		}));

		render(CodexProfiles, {
			route: { kind: "fork", profileID: "work" },
			profiles: [codexDetail.summary],
			dashboardConfigSets: [codexDetail.config_set!],
			detectResult: validCodexDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validCodexDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		const profileID = await screen.findByLabelText("Profile ID");
		await user.clear(profileID);
		await user.type(profileID, destinationProfile.id);
		await waitFor(() => {
			expect(screen.getByLabelText("Name")).toHaveValue(destinationProfile.name);
			expect(screen.getByLabelText("Description")).toHaveValue(destinationProfile.description);
		});
		await user.click(screen.getByRole("button", { name: "Fork" }));

		await waitFor(() => expect(backend.forkCodexProfile).toHaveBeenCalledOnce());
		expect(backend.listProfiles).toHaveBeenCalledTimes(2);
		expect(backend.forkCodexProfile).toHaveBeenCalledWith(expect.objectContaining({
			profile_id: destinationProfile.id,
			name: null,
			description: null,
		}));
	});

	it("passes explicitly edited Codex Fork metadata to an existing destination", async () => {
		const user = userEvent.setup();
		backend.showCodexProfile.mockReturnValue(cancellableResolved(codexDetail));
		backend.listProfiles.mockReturnValue(cancellableResolved([destinationProfile]));
		backend.forkCodexProfile.mockReturnValue(cancellableResolved({
			profile: destinationProfile,
			warnings: [],
		}));

		render(CodexProfiles, {
			route: { kind: "fork", profileID: "work" },
			profiles: [codexDetail.summary],
			dashboardConfigSets: [codexDetail.config_set!],
			detectResult: validCodexDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validCodexDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		const profileID = await screen.findByLabelText("Profile ID");
		await user.clear(profileID);
		await user.type(profileID, destinationProfile.id);
		const name = screen.getByLabelText("Name");
		await waitFor(() => expect(name).toHaveValue(destinationProfile.name));
		await user.clear(name);
		expect(await screen.findByText("Name is required.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Fork" })).toBeDisabled();
		expect(backend.forkCodexProfile).not.toHaveBeenCalled();
		await user.type(name, "Updated shared Profile");
		const description = screen.getByLabelText("Description");
		expect(description).toHaveValue(destinationProfile.description);
		await user.clear(description);
		await user.click(screen.getByRole("button", { name: "Fork" }));

		await waitFor(() => expect(backend.forkCodexProfile).toHaveBeenCalledOnce());
		expect(backend.forkCodexProfile).toHaveBeenCalledWith(expect.objectContaining({
			profile_id: destinationProfile.id,
			name: "Updated shared Profile",
			description: "",
		}));
	});

	it("preserves existing destination metadata when Grok Build Fork defaults are untouched", async () => {
		const user = userEvent.setup();
		backend.showGrokBuildProfile.mockReturnValue(cancellableResolved(grokBuildDetail));
		backend.listProfiles.mockReturnValue(cancellableResolved([destinationProfile]));
		backend.forkGrokBuildProfile.mockReturnValue(cancellableResolved({
			profile: destinationProfile,
			warnings: [],
		}));

		render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "fork", profileID: "work" },
			profiles: [grokBuildDetail.summary],
			dashboardConfigSets: [grokBuildDetail.config_set!],
			detectResult: validGrokBuildDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validGrokBuildDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		const profileID = await screen.findByLabelText("Profile ID");
		await user.clear(profileID);
		await user.type(profileID, destinationProfile.id);
		await waitFor(() => {
			expect(screen.getByLabelText("Name")).toHaveValue(destinationProfile.name);
			expect(screen.getByLabelText("Description")).toHaveValue(destinationProfile.description);
		});
		await user.click(screen.getByRole("button", { name: "Fork" }));

		await waitFor(() => expect(backend.forkGrokBuildProfile).toHaveBeenCalledOnce());
		expect(backend.listProfiles).toHaveBeenCalledTimes(2);
		expect(backend.forkGrokBuildProfile).toHaveBeenCalledWith(expect.objectContaining({
			profile_id: destinationProfile.id,
			name: null,
			description: null,
		}));
	});

	it("validates and passes edited Grok Build Fork metadata for an existing destination", async () => {
		const user = userEvent.setup();
		backend.showGrokBuildProfile.mockReturnValue(cancellableResolved(grokBuildDetail));
		backend.listProfiles.mockReturnValue(cancellableResolved([destinationProfile]));
		backend.forkGrokBuildProfile.mockReturnValue(cancellableResolved({
			profile: destinationProfile,
			warnings: [],
		}));

		render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "fork", profileID: "work" },
			profiles: [grokBuildDetail.summary],
			dashboardConfigSets: [grokBuildDetail.config_set!],
			detectResult: validGrokBuildDetect,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validGrokBuildDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		const profileID = await screen.findByLabelText("Profile ID");
		await user.clear(profileID);
		await user.type(profileID, destinationProfile.id);
		const name = screen.getByLabelText("Name");
		await waitFor(() => expect(name).toHaveValue(destinationProfile.name));
		await user.clear(name);
		expect(await screen.findByText("Name is required.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Fork" })).toBeDisabled();
		expect(backend.forkGrokBuildProfile).not.toHaveBeenCalled();

		await user.type(name, "Updated Grok Build Profile");
		const description = screen.getByLabelText("Description");
		expect(description).toHaveValue(destinationProfile.description);
		await user.clear(description);
		await user.type(description, "Updated Grok Build description");
		await user.click(screen.getByRole("button", { name: "Fork" }));

		await waitFor(() => expect(backend.forkGrokBuildProfile).toHaveBeenCalledOnce());
		expect(backend.forkGrokBuildProfile).toHaveBeenCalledWith(expect.objectContaining({
			profile_id: destinationProfile.id,
			name: "Updated Grok Build Profile",
			description: "Updated Grok Build description",
		}));
	});

	it("blocks Grok Build save-current when config.toml is missing", async () => {
		const user = userEvent.setup();
		const missingConfig = { ...validGrokBuildDetect, config_status: "missing" };
		const refreshDetect = vi.fn().mockResolvedValue(missingConfig);
		backend.showGrokBuildProfile.mockReturnValue(cancellableResolved(grokBuildDetail));

		render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "list", profileID: "" },
			profiles: [grokBuildDetail.summary],
			dashboardConfigSets: [grokBuildDetail.config_set!],
			detectResult: missingConfig,
			detectError: "",
			activeProfileID: "work",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect,
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		await user.click(screen.getAllByRole("button", { name: "More actions" }).at(-1)!);
		await user.click(await screen.findByText("Save Current Login and Settings"));

		const dialog = await screen.findByRole("alertdialog");
		const message = "Grok Build config.toml is missing. Restore or recreate it before saving. ProfileDeck did not change the saved login or settings.";
		expect(within(dialog).getByText(message)).toBeInTheDocument();

		const detectCallsBeforeSave = refreshDetect.mock.calls.length;
		await user.click(within(dialog).getByRole("button", { name: "Save Current Login and Settings" }));
		await waitFor(() => expect(refreshDetect).toHaveBeenCalledTimes(detectCallsBeforeSave + 1));
		expect(within(dialog).getByText(message)).toBeInTheDocument();
		expect(backend.saveGrokBuildProfile).not.toHaveBeenCalled();
	});

	it("keeps an open Grok Build detail page in sync with the active Profile", async () => {
		const inactiveDetail = {
			...grokBuildDetail,
			summary: { ...grokBuildDetail.summary, active: false },
		};
		backend.showGrokBuildProfile.mockReturnValue(cancellableResolved(inactiveDetail));
		const view = render(GrokBuildProfiles, {
			...grokBuildQuotaProps(),
			route: { kind: "detail", profileID: "work" },
			profiles: [inactiveDetail.summary],
			dashboardConfigSets: [grokBuildDetail.config_set!],
			detectResult: validGrokBuildDetect,
			detectError: "",
			activeProfileID: "personal",
			loadingProfiles: false,
			profileError: "",
			useRequest: null,
			refreshDetect: vi.fn().mockResolvedValue(validGrokBuildDetect),
			refreshProfiles: vi.fn().mockResolvedValue(undefined),
			cancelDetect: vi.fn(),
			onUseRequestHandled: vi.fn(),
			showError: vi.fn(),
			showNotice: vi.fn(),
		}, { wrapper: ProfileTestProviders });

		await screen.findByRole("heading", { name: "Work" });
		expect(screen.getByRole("button", { name: "Use Profile" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Refresh credits" })).not.toBeInTheDocument();

		await view.rerender({
			profiles: [grokBuildDetail.summary],
			activeProfileID: "work",
		});
		await waitFor(() => {
			expect(screen.queryByRole("button", { name: "Use Profile" })).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: "Refresh credits" })).toBeInTheDocument();
		});

		await view.rerender({
			profiles: [inactiveDetail.summary],
			activeProfileID: "personal",
		});
		await waitFor(() => {
			expect(screen.getByRole("button", { name: "Use Profile" })).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Refresh credits" })).not.toBeInTheDocument();
		});
	});
});
