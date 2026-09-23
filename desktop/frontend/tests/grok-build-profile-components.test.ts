import { render, screen, within } from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "svelte";
import { tick } from "svelte";
import { locale } from "svelte-i18n";
import { describe, expect, it, vi } from "vitest";

import type { SwitchPlan } from "../bindings/github.com/strahe/profiledeck/internal/switching/models";
import ProfileEditorPage from "../src/features/profiles/ProfileEditorPage.svelte";
import ProfileList from "../src/features/profiles/ProfileList.svelte";
import UseProfileDialog from "../src/features/profiles/UseProfileDialog.svelte";
import type { ManagedProfileListItem } from "../src/features/profiles/types";
import TestProviders from "./TestProviders.svelte";

const noop = vi.fn();

const profile: ManagedProfileListItem = {
	id: "work",
	name: "Work",
	description: "Primary Grok Build Profile",
	summary: {
		profile: {
			id: "work",
			name: "Work",
			description: "Primary Grok Build Profile",
			metadata: {},
			created_at_unix_ms: 1,
			updated_at_unix_ms: 1,
		},
		provider_id: "grok-build",
		credential_reference_count: 1,
		config_set_reference_count: 1,
		active: false,
		updated_at_unix_ms: 1,
		warnings: ["Grok Build login binding is invalid"],
	},
};

function renderList(overrides: Partial<ComponentProps<typeof ProfileList>> = {}) {
	return render(ProfileList, {
		profiles: [],
		loading: false,
		error: "",
		busy: false,
		canCreate: true,
		emptyDescription: "Sign in to Grok Build before saving a Profile.",
		createLabel: "Save Current Grok Build as a New Profile",
		onNew: noop,
		onUse: noop,
		onDetails: noop,
		onFork: noop,
		onDelete: noop,
		...overrides,
	}, { wrapper: TestProviders });
}

describe("Grok Build managed Profile components", () => {
	it("saves from the current Profile menu", async () => {
		const user = userEvent.setup();
		const onSaveCurrent = vi.fn();
		const current = {
			...profile,
			id: "current",
			name: "Current",
			summary: { ...profile.summary, profile: { ...profile.summary.profile, id: "current", name: "Current" }, active: true },
		};
		renderList({ profiles: [current, profile], onSaveCurrent, saveCurrentLabel: "Save Current Login and Settings" });
		const menus = screen.getAllByRole("button", { name: "More actions" });
		await user.click(menus[0]);
		await user.click(await screen.findByText("Save Current Login and Settings"));
		expect(onSaveCurrent).toHaveBeenCalledWith(current);
	});

	it("hides save in an inactive Profile menu", async () => {
		const user = userEvent.setup();
		renderList({ profiles: [profile], onSaveCurrent: vi.fn(), saveCurrentLabel: "Save Current Login and Settings" });
		await user.click(screen.getByRole("button", { name: "More actions" }));
		expect(screen.queryByRole("menuitem", { name: "Save Current Login and Settings" })).not.toBeInTheDocument();
	});

	it("disables current Profile save while busy", async () => {
		const user = userEvent.setup();
		renderList({ profiles: [{ ...profile, summary: { ...profile.summary, active: true } }], busy: true, onSaveCurrent: vi.fn(), saveCurrentLabel: "Save Current Login and Settings" });
		await user.click(screen.getByRole("button", { name: "More actions" }));
		expect(await screen.findByText("Save Current Login and Settings")).toHaveAttribute("data-disabled");
	});

	it("covers loading, empty, error, warning, and populated list states", () => {
		let view = render(ProfileList, {
			profiles: [],
			loading: true,
			error: "",
			busy: false,
			onUse: noop,
			onDetails: noop,
			onFork: noop,
			onDelete: noop,
		}, { wrapper: TestProviders });
		expect(document.querySelectorAll("[data-slot=skeleton]")).not.toHaveLength(0);
		view.unmount();

		view = renderList();
		expect(screen.getByText("Sign in to Grok Build before saving a Profile.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Save Current Grok Build as a New Profile" })).toBeInTheDocument();
		view.unmount();

		view = renderList({ error: "Grok Build Profile data is unavailable." });
		expect(screen.getByText("Grok Build Profile data is unavailable.")).toBeInTheDocument();
		view.unmount();

		renderList({ profiles: [profile] });
		expect(screen.getByRole("button", { name: "Work" })).toBeInTheDocument();
		expect(screen.getByText("Profile warning")).toBeInTheDocument();
		expect(screen.getByText("This Profile's saved login is missing or cannot be used.")).toBeInTheDocument();
	});

	it("disables creation when file-backed authentication is unavailable", () => {
		render(ProfileEditorPage, {
			mode: "new",
			detectResult: {
				profiledeck_initialized: true,
				provider_compatible: true,
				file_auth_supported: false,
				config_status: "valid",
				auth_status: "valid",
			},
			basePath: "/grok-build/profiles",
			copyRoot: "grokBuild.profilePages",
			allowMissingConfig: true,
			sourceError: "Grok Build is using a custom authentication source. Unset GROK_AUTH and GROK_AUTH_PATH before managing file-backed Profiles.",
			createActionLabel: "Save Current Grok Build as a New Profile",
			busy: false,
			profileID: "work",
			profileName: "Work",
			idError: "",
			nameError: "",
			descriptionError: "",
			onCancel: noop,
			onSubmit: noop,
		}, { wrapper: TestProviders });

		expect(screen.getByRole("button", { name: "Save Current Grok Build as a New Profile" })).toBeDisabled();
		expect(screen.getByText(/Unset GROK_AUTH and GROK_AUTH_PATH/)).toBeInTheDocument();
	});

	it("requires the current config only when Profile creation captures it", () => {
		const props: ComponentProps<typeof ProfileEditorPage> = {
			mode: "new",
			detectResult: {
				profiledeck_initialized: true,
				provider_compatible: true,
				file_auth_supported: true,
				config_status: "invalid",
				auth_status: "valid",
			},
			basePath: "/grok-build/profiles",
			copyRoot: "grokBuild.profilePages",
			allowMissingConfig: true,
			requiresCurrentConfig: false,
			createActionLabel: "Save Current Grok Build as a New Profile",
			busy: false,
			profileID: "work",
			profileName: "Work",
			idError: "",
			nameError: "",
			descriptionError: "",
			onCancel: noop,
			onSubmit: noop,
		};
		const view = render(ProfileEditorPage, props, { wrapper: TestProviders });
		expect(screen.getByRole("button", { name: "Save Current Grok Build as a New Profile" })).toBeEnabled();
		view.unmount();

		render(ProfileEditorPage, { ...props, requiresCurrentConfig: true }, { wrapper: TestProviders });
		expect(screen.getByRole("button", { name: "Save Current Grok Build as a New Profile" })).toBeDisabled();
	});

	it("links the fork breadcrumb to Grok Build Profiles", () => {
		render(ProfileEditorPage, {
			mode: "fork",
			detectResult: null,
			basePath: "/grok-build/profiles",
			copyRoot: "grokBuild.profilePages",
			busy: false,
			idError: "",
			nameError: "",
			descriptionError: "",
			onCancel: noop,
			onSubmit: noop,
		}, { wrapper: TestProviders });

		expect(screen.getByRole("link", { name: "Profiles" })).toHaveAttribute("href", "#/grok-build/profiles");
	});

	it("describes Fork as a destination Profile in English and Simplified Chinese", async () => {
		locale.set("en");
		await tick();
		let view = render(ProfileEditorPage, {
			mode: "fork",
			detectResult: null,
			basePath: "/grok-build/profiles",
			busy: false,
			idError: "",
			nameError: "",
			descriptionError: "",
			onCancel: noop,
			onSubmit: noop,
		}, { wrapper: TestProviders });
		expect(screen.getByText("Choose a destination Profile and which login and settings to share or copy. You can reuse a Profile that does not already contain data for this Agent.")).toBeInTheDocument();
		view.unmount();

		locale.set("zh-CN");
		await tick();
		view = render(ProfileEditorPage, {
			mode: "fork",
			detectResult: null,
			basePath: "/grok-build/profiles",
			busy: false,
			idError: "",
			nameError: "",
			descriptionError: "",
			onCancel: noop,
			onSubmit: noop,
		}, { wrapper: TestProviders });
		expect(screen.getByText("选择目标 Profile，并决定共享或复制哪些登录和设置。若已有 Profile 尚未包含当前 Agent 的数据，也可以直接使用。")).toBeInTheDocument();
		view.unmount();
		locale.set("en");
		await tick();
	});

	it("shows only Grok Build actions, paths, and warnings in switch review", async () => {
		const secretAuth = "secret-auth-body-must-stay-hidden";
		const secretConfig = "secret-config-body-must-stay-hidden";
		const plan: SwitchPlan = {
			created_at_unix_ms: 1,
			read_only: true,
			plan_fingerprint: "fingerprint",
			provider: { id: "grok-build", name: "Grok Build", adapter_id: "grok-build" },
			profile: { id: "work", name: "Work", description: "" },
			bindings: [
				{ target_id: "auth", target_resource_id: "credential", changed: true },
				{ target_id: "config", target_resource_id: "config-set", changed: true },
			],
			state_captures: [],
			operations: [
				{
					provider_id: "grok-build",
					profile_id: "work",
					target_id: "auth",
					backend_id: "file",
					target_label: "Grok Build login",
					path: "/temporary/grok/auth.json",
					format: "json",
					strategy: "replace",
					action: "update",
					status_reason: "",
					file_exists: true,
					is_symlink: false,
					before_sha256: "",
					desired_sha256: "",
					before_preview: { content: secretAuth, truncated: false },
					desired_preview: { content: secretAuth, truncated: false },
					after_preview: { content: secretAuth, truncated: false },
					warnings: ["finish active Grok Build sessions before applying file changes"],
				},
				{
					provider_id: "grok-build",
					profile_id: "work",
					target_id: "config",
					backend_id: "file",
					target_label: "Grok Build settings",
					path: "/temporary/grok/config.toml",
					format: "toml",
					strategy: "replace",
					action: "create",
					status_reason: "",
					file_exists: false,
					is_symlink: false,
					before_sha256: "",
					desired_sha256: "",
					before_preview: { content: secretConfig, truncated: false },
					desired_preview: { content: secretConfig, truncated: false },
					after_preview: { content: secretConfig, truncated: false },
					warnings: [],
				},
			],
			warnings: [],
		};

		render(UseProfileDialog, {
			open: true,
			profile,
			agent: "Grok Build",
			mode: "grok-build",
			currentProfile: "personal",
			plan,
			building: false,
			applying: false,
			inlineError: "",
			onClose: noop,
			onConfirm: noop,
		}, { wrapper: TestProviders });

		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText("/temporary/grok/auth.json")).toBeInTheDocument();
		expect(within(dialog).getByText("/temporary/grok/config.toml")).toBeInTheDocument();
		expect(within(dialog).getByText("End active Grok Build sessions before applying file changes.")).toBeInTheDocument();
		expect(dialog).not.toHaveTextContent(secretAuth);
		expect(dialog).not.toHaveTextContent(secretConfig);
		expect(within(dialog).queryByText("Before")).not.toBeInTheDocument();
		expect(within(dialog).queryByText("After")).not.toBeInTheDocument();
	});
});
