import { render, screen, waitFor, within } from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";
import { locale } from "svelte-i18n";
import { tick } from "svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CodexDetectResult } from "../bindings/github.com/strahe/profiledeck/internal/codex/models";
import type {
	DetectResult as GrokBuildDetectResult,
	ProfileDetail as GrokBuildProfileDetail,
} from "../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";

const backend = vi.hoisted(() => ({
	createCodexProfile: vi.fn(),
	createGrokBuildProfile: vi.fn(),
	saveGrokBuildProfile: vi.fn(),
	showGrokBuildProfile: vi.fn(),
	deleteProfile: vi.fn(),
}));

vi.mock("../bindings/github.com/strahe/profiledeck/desktop/backend", () => ({
	CodexService: {
		CreateProfile: backend.createCodexProfile,
	},
	GrokBuildService: {
		CreateProfile: backend.createGrokBuildProfile,
		SaveActiveProfileState: backend.saveGrokBuildProfile,
		ShowProfile: backend.showGrokBuildProfile,
	},
	ProfileService: {
		Delete: backend.deleteProfile,
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

beforeEach(async () => {
	vi.clearAllMocks();
	locale.set("en");
	await tick();
});

describe("managed Profile mutation guards", () => {
	it("shows Grok Build create recheck failures and does not create a Profile", async () => {
		const user = userEvent.setup();
		const invalid = { ...validGrokBuildDetect, auth_status: "invalid" };
		const refreshDetect = vi.fn()
			.mockResolvedValueOnce(validGrokBuildDetect)
			.mockResolvedValueOnce(invalid)
			.mockResolvedValueOnce(validGrokBuildDetect);

		render(GrokBuildProfiles, {
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
	});

	it("shows Codex create recheck failures and does not create a Profile", async () => {
		const user = userEvent.setup();
		const refreshDetect = vi.fn()
			.mockResolvedValueOnce(validCodexDetect)
			.mockResolvedValueOnce(null)
			.mockResolvedValueOnce(validCodexDetect);

		render(CodexProfiles, {
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
	});

	it("blocks Grok Build save-current when config.toml is missing", async () => {
		const user = userEvent.setup();
		const missingConfig = { ...validGrokBuildDetect, config_status: "missing" };
		backend.showGrokBuildProfile.mockReturnValue(cancellableResolved(grokBuildDetail));
		const refreshDetect = vi.fn().mockResolvedValue(missingConfig);

		render(GrokBuildProfiles, {
			route: { kind: "detail", profileID: "work" },
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

		await screen.findByRole("heading", { name: "Work" });
		await user.click(screen.getByRole("button", { name: "More actions" }));
		await user.click(await screen.findByText("Update from Current Grok Build"));

		const dialog = await screen.findByRole("alertdialog");
		const message = "Grok Build config.toml is missing. Restore or recreate it before updating this Profile. ProfileDeck did not change the saved login or settings.";
		expect(within(dialog).getByText(message)).toBeInTheDocument();

		await user.click(within(dialog).getByRole("button", { name: "Update from Current Grok Build" }));
		await waitFor(() => expect(refreshDetect).toHaveBeenCalledOnce());
		expect(within(dialog).getByText(message)).toBeInTheDocument();
		expect(backend.saveGrokBuildProfile).not.toHaveBeenCalled();
	});
});
