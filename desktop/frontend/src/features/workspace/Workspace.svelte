<script lang="ts">
	import { onMount } from "svelte";
	import { CancelError, Events, type CancellablePromise } from "@wailsio/runtime";
	import { setMode } from "mode-watcher";
	import { push } from "svelte-spa-router";
	import { _ } from "svelte-i18n";
	import { get } from "svelte/store";
	import { toast } from "svelte-sonner";

	import {
		AppService,
		CodexService,
		DoctorService,
		SettingsService,
		SwitchService,
		UsageService,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CreateCodexProfileRequest,
		DashboardResult,
		DesktopError,
		ForkCodexProfileRequest,
		SyncCodexProfileRequest,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexDetectResult,
		CodexProfileDetail,
		CodexProfileDraft,
		CodexProfileSummary,
		DoctorResult,
		PlanOperation,
		SwitchPlan,
		UsageSummaryResult,
		UsageSyncResult,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	import * as Dialog from "$lib/components/ui/dialog";
	import * as Empty from "$lib/components/ui/empty";
	import * as Field from "$lib/components/ui/field";
	import * as Table from "$lib/components/ui/table";
	import * as Tabs from "$lib/components/ui/tabs";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Spinner } from "$lib/components/ui/spinner";
	import {
		applyDesktopLanguagePreference,
		normalizeDesktopLanguage,
		type DesktopLanguage,
	} from "$lib/i18n";
	import { cn } from "$lib/utils";

	type AgentID = "codex" | "claude" | "gemini" | "opencode";
	type PlaceholderAgentID = Exclude<AgentID, "codex">;
	type WorkspaceTab = "profiles" | "health" | "usage";
	type WorkspaceView = WorkspaceTab | "settings";
	type ProfileRoute = "list" | "new" | "detail" | "fork";
	type StatusVariant = "ok" | "warn" | "idle" | "muted";
	type Platform = "macos" | "windows" | "linux";

	type DesktopChangeEvent = {
		kind: string;
		source?: string;
		status?: "success" | "failure" | "canceled" | string;
		error?: DesktopError | null;
		provider_id?: string;
		profile_id?: string;
		operation_id?: string;
	};

	type DashboardUpdatePayload = {
		event: DesktopChangeEvent;
		dashboard: DashboardResult;
		error?: DesktopError | null;
	};

	type ErrorShape = DesktopError & {
		Code?: string;
		Message?: string;
		Cause?: unknown;
		Details?: Record<string, unknown> | null;
		message?: string;
		code?: string;
		name?: string;
		cause?: unknown;
	};

	type AgentProfile = {
		id: string;
		name: string;
		desc: string;
		updated: string;
		model: string;
		provider: string;
		baseURL: string;
		accountID: string;
		accountLabel: string;
		summary?: CodexProfileSummary;
	};

	type AgentData = {
		id: AgentID;
		name: string;
		detected: boolean;
		current: string;
		targets: string[];
		profiles: AgentProfile[];
		usage: {
			events: string;
			input: string;
			output: string;
			cost: string;
		};
		health: {
			overall: string;
			lock: string;
			pending: number;
			failed: number;
			findings: Array<{ id: string; status: string; message: string }>;
		};
		placeholder: boolean;
	};

	type PendingUse = {
		agentID: AgentID;
		profileID: string;
		profileName: string;
		plan?: SwitchPlan | null;
	};

	type CodexForkAuthBinding = "share-parent" | "copy-new";
	type CodexSyncAuthUpdate = "" | "update-shared" | "fork-new";

	const codexProviderID = "codex";
	const tabs: Array<{ id: WorkspaceTab; labelKey: string }> = [
		{ id: "profiles", labelKey: "tabs.profiles" },
		{ id: "health", labelKey: "tabs.health" },
		{ id: "usage", labelKey: "tabs.usage" },
	];
	const agentOrder: AgentID[] = ["codex", "claude", "gemini", "opencode"];
	const desktopChangeKind = {
		codexProfileChanged: "codex-profile-changed",
		switchApplied: "switch-applied",
		rollbackApplied: "rollback-applied",
		switchRecovered: "switch-recovered",
		usageSynced: "usage-synced",
	} as const;

	let {
		initialView = "profiles",
		profileRoute = "list",
		params = {},
	}: {
		initialView?: WorkspaceView;
		profileRoute?: ProfileRoute;
		params?: { id?: string };
	} = $props();

	const initialPlaceholders: Record<PlaceholderAgentID, AgentData> = {
		claude: {
			id: "claude",
			name: "Claude",
			detected: true,
			current: "personal",
			targets: ["~/.claude/settings.json", "~/.claude/auth.json"],
			profiles: [
				{
					id: "personal",
					name: "personal",
					desc: "Personal account",
					updated: "Yesterday 20:18",
					model: "claude-sonnet-4-6",
					provider: "anthropic",
					baseURL: "",
					accountID: "",
					accountLabel: "No account",
				},
				{
					id: "client-a",
					name: "client-a",
					desc: "Client A workspace",
					updated: "Yesterday 19:42",
					model: "claude-sonnet-4-6",
					provider: "anthropic",
					baseURL: "",
					accountID: "",
					accountLabel: "No account",
				},
			],
			usage: { events: "8,914", input: "182M", output: "9.2M", cost: "—" },
			health: { overall: "OK", lock: "OK", pending: 0, failed: 0, findings: [] },
			placeholder: true,
		},
		gemini: {
			id: "gemini",
			name: "Gemini",
			detected: false,
			current: "",
			targets: ["~/.gemini/config.json"],
			profiles: [],
			usage: { events: "—", input: "—", output: "—", cost: "—" },
			health: { overall: "—", lock: "—", pending: 0, failed: 0, findings: [] },
			placeholder: true,
		},
		opencode: {
			id: "opencode",
			name: "OpenCode",
			detected: false,
			current: "",
			targets: ["~/.opencode/config.toml"],
			profiles: [],
			usage: { events: "—", input: "—", output: "—", cost: "—" },
			health: { overall: "—", lock: "—", pending: 0, failed: 0, findings: [] },
			placeholder: true,
		},
	};

	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let selectedAgent = $state<AgentID>("codex");
	let selectedView = $state<WorkspaceView>("profiles");
	let selectedTab = $state<WorkspaceTab>("profiles");
	let currentRouteView = $state<WorkspaceView>("profiles");
	let currentProfileRoute = $state<ProfileRoute>("list");
	let currentProfileID = $state("");
	let theme = $state<"light" | "dark">("light");
	let platform = $state<Platform>(detectPlatform());
	let loading = $state(false);
	let loadingProfiles = $state(false);
	let actionBusy = $state("");
	let languageBusy = $state(false);
	let languagePreference = $state<DesktopLanguage>("auto");
	let lastToast = "";

	let dashboard = $state<DashboardResult | null>(null);
	let detectResult = $state<CodexDetectResult | null>(null);
	let doctorResult = $state<DoctorResult | null>(null);
	let usageSummary = $state<UsageSummaryResult | null>(null);
	let usageSyncResult = $state<UsageSyncResult | null>(null);
	let codexProfileSummaries = $state<CodexProfileSummary[]>([]);
	let dashboardError = $state("");
	let detectError = $state("");
	let profileError = $state("");
	let profileEditorError = $state("");
	let profileEditorLoading = $state(false);
	let usageError = $state("");
	let lastRefreshSummary = $state("");
	let lastProfileRouteKey = "";

	let placeholderAgents = $state<Record<PlaceholderAgentID, AgentData>>(initialPlaceholders);

	let profileDraft = $state<CodexProfileDraft | null>(null);
	let profileDetail = $state<CodexProfileDetail | null>(null);
	let profileFormID = $state("");
	let profileFormName = $state("");
	let profileFormDescription = $state("");
	let profileConfigContent = $state("");
	let profileAuthContent = $state("");
	let forkAuthBinding = $state<CodexForkAuthBinding>("share-parent");
	let syncAuthUpdate = $state<CodexSyncAuthUpdate>("");

	let useConfirmOpen = $state(false);
	let pendingUse = $state<PendingUse | null>(null);

	let codexAgent = $derived(buildCodexAgent());
	let currentAgent = $derived(agentByID(selectedAgent));

	$effect(() => {
		const nextProfileRoute = initialView === "profiles" ? profileRoute : "list";
		const nextProfileID = params?.id ?? "";
		const nextKey = `${initialView}:${nextProfileRoute}:${nextProfileID}`;
		if (lastProfileRouteKey === nextKey) return;
		lastProfileRouteKey = nextKey;
		currentRouteView = initialView;
		currentProfileRoute = nextProfileRoute;
		currentProfileID = nextProfileID;
		selectedView = initialView;
		if (initialView !== "settings") {
			selectedAgent = "codex";
			selectedTab = initialView;
		}
		if (initialView === "profiles") {
			void enterProfileRoute(nextProfileRoute, nextProfileID);
		}
	});

	onMount(() => {
		applyTheme(theme);
		lastRefreshSummary = t("diagnostics.waiting");
		applyDesktopLanguagePreference(languagePreference);
		void loadSettings();
		void refreshAll();

		const off = [
			Events.On("profiledeck:open-switch", (event) => {
				const payload = event.data ?? {};
				if (payload.provider_id !== codexProviderID || !payload.profile_id) return;
				selectedAgent = "codex";
				void navigateToView("profiles");
				void openUseByProfileID(payload.profile_id);
			}),
			Events.On("profiledeck:open-doctor", () => {
				selectedAgent = "codex";
				void navigateToView("health");
				void runHealth();
			}),
			Events.On("profiledeck:usage-synced", (event) => {
				usageSyncResult = event.data ?? null;
				showNotice(t("notice.usageSynced.title"), t("notice.usageSynced.codexDescription"));
			}),
			Events.On("profiledeck:dashboard-updated", (event) => {
				handleDashboardUpdate(event.data as DashboardUpdatePayload);
			}),
			Events.On("profiledeck:operation-error", (event) => {
				if (!isCancelError(event.data)) showError(event.data);
			}),
		];

		return () => {
			for (const dispose of off) dispose();
			cancelAll();
		};
	});

	async function refreshAll() {
		loading = true;
		lastRefreshSummary = t("diagnostics.loading");
		try {
			try {
				const nextDashboard = await track("dashboard", AppService.Dashboard());
				applyDashboardResult(nextDashboard);
				dashboardError = "";
			} catch (err) {
				if (!isCancelError(err)) {
					dashboardError = formatError(err);
					showError(err);
				}
			}
			await Promise.allSettled([
				refreshDetect(),
				refreshCodexProfiles(),
				refreshUsage(),
			]);
		} catch (err) {
			if (!isCancelError(err)) showError(err);
		} finally {
			updateRefreshSummary();
			loading = false;
		}
	}

	async function loadSettings() {
		try {
			const settings = await track("settings", SettingsService.Get());
			languagePreference = applyDesktopLanguagePreference(settings.language);
		} catch (err) {
			if (!isCancelError(err)) showError(err);
		}
	}

	async function changeLanguage(value: string) {
		const next = normalizeDesktopLanguage(value);
		languagePreference = applyDesktopLanguagePreference(next);
		languageBusy = true;
		try {
			const settings = await track("settings-update", SettingsService.Update({ config_dir: "", language: next }));
			languagePreference = applyDesktopLanguagePreference(settings.language);
			showNotice(t("notice.settingsSaved.title"), t("notice.settingsSaved.description"));
		} catch (err) {
			if (!isCancelError(err)) showError(err);
		} finally {
			languageBusy = false;
		}
	}

	async function navigateToView(view: WorkspaceView) {
		selectedView = view;
		currentRouteView = view;
		if (view !== "settings") selectedTab = view;
		await push(viewRoute(view));
	}

	function viewRoute(view: WorkspaceView): string {
		if (view === "settings") return "/settings";
		return `/codex/${view}`;
	}

	async function selectAgent(agentID: AgentID) {
		selectedAgent = agentID;
		await navigateToView("profiles");
	}

	async function refreshDetect() {
		try {
			detectResult = await track("detect", CodexService.Detect());
			detectError = "";
		} catch (err) {
			if (!isCancelError(err)) {
				detectResult = null;
				detectError = formatError(err);
			}
		}
	}

	async function refreshDashboard() {
		try {
			const nextDashboard = await track("dashboard-light", AppService.Dashboard());
			applyDashboardResult(nextDashboard);
			dashboardError = "";
		} catch (err) {
			if (!isCancelError(err)) {
				dashboardError = formatError(err);
				showError(err);
			}
		} finally {
			updateRefreshSummary();
		}
	}

	async function refreshCodexProfiles() {
		loadingProfiles = true;
		profileError = "";
		try {
			const result = await track("codex-profiles", CodexService.ListProfiles());
			codexProfileSummaries = result.profiles ?? [];
			profileError = "";
		} catch (err) {
			if (!isCancelError(err)) {
				profileError = formatError(err);
			}
		} finally {
			loadingProfiles = false;
		}
	}

	async function refreshUsage() {
		try {
			usageSummary = await track("usage-summary", UsageService.Summary(codexProviderID));
			usageError = "";
		} catch (err) {
			if (!isCancelError(err)) {
				usageSummary = null;
				usageError = formatError(err);
			}
		}
	}

	async function detectSelectedAgent() {
		if (selectedAgent !== "codex") {
			showNotice(t("notice.detected.title"), t("notice.detected.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await runAction("detect", async () => {
			detectResult = await track("detect", CodexService.Detect());
			detectError = "";
			updateRefreshSummary();
			showNotice(t("notice.detected.title"), t("notice.detected.codexDescription"));
		});
	}

	async function refreshSelectedAgent() {
		if (selectedAgent !== "codex") {
			showNotice(t("notice.refreshed.title"), t("notice.refreshed.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await refreshAll();
	}

	async function openNewProfile() {
		if (selectedAgent !== "codex") {
			showNotice(t("notice.profileSaved.title"), t("notice.profileSaved.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await push("/codex/profiles/new");
	}

	async function openProfileDetails(profile: AgentProfile) {
		await push(`/codex/profiles/${encodeURIComponent(profile.id)}`);
	}

	async function openForkProfile(profile: AgentProfile) {
		await push(`/codex/profiles/${encodeURIComponent(profile.id)}/fork`);
	}

	async function backToProfiles() {
		await push("/codex/profiles");
	}

	async function enterProfileRoute(route: ProfileRoute, profileID: string) {
		profileEditorError = "";
		if (route === "list") {
			resetProfileEditor();
			return;
		}
		if (route === "new") {
			resetProfileEditor();
			await loadCurrentProfileDraft();
			return;
		}
		if (!profileID) {
			profileEditorError = t("errors.profileNotReady");
			return;
		}
		if (route === "detail") {
			await loadStoredProfileDraft(profileID);
			return;
		}
		await loadForkDraft(profileID);
	}

	async function loadCurrentProfileDraft() {
		profileEditorLoading = true;
		profileEditorError = "";
		try {
			const draft = await track("codex-profile-current-draft", CodexService.LoadProfileDraft());
			profileDraft = draft;
			profileConfigContent = draft.config_content;
			profileAuthContent = draft.auth_content;
		} catch (err) {
			if (!isCancelError(err)) profileEditorError = formatError(err);
		} finally {
			profileEditorLoading = false;
		}
	}

	async function loadStoredProfileDraft(profileID: string) {
		profileEditorLoading = true;
		profileEditorError = "";
		try {
			const [detail, draft] = await Promise.all([
				track("codex-profile-detail", CodexService.ShowProfile(profileID)),
				track("codex-profile-stored-draft", CodexService.LoadStoredProfileDraft(profileID)),
			]);
			profileDetail = detail;
			profileDraft = draft;
			profileFormID = detail.summary.profile.id;
			profileFormName = detail.summary.profile.name || detail.summary.profile.id;
			profileFormDescription = detail.summary.profile.description || "";
			profileConfigContent = draft.config_content;
			profileAuthContent = draft.auth_content;
			syncAuthUpdate = "";
		} catch (err) {
			if (!isCancelError(err)) profileEditorError = formatError(err);
		} finally {
			profileEditorLoading = false;
		}
	}

	async function loadForkDraft(profileID: string) {
		profileEditorLoading = true;
		profileEditorError = "";
		try {
			const detail = await track("codex-profile-fork-detail", CodexService.ShowProfile(profileID));
			profileDetail = detail;
			const sourceName = detail.summary.profile.name || detail.summary.profile.id;
			profileFormID = `${detail.summary.profile.id}-copy`;
			profileFormName = `${sourceName} copy`;
			profileFormDescription = detail.summary.profile.description || "";
			forkAuthBinding = "share-parent";
		} catch (err) {
			if (!isCancelError(err)) profileEditorError = formatError(err);
		} finally {
			profileEditorLoading = false;
		}
	}

	async function createCodexProfileFromEditor() {
		const req: CreateCodexProfileRequest = {
			profile_id: profileFormID,
			name: optional(profileFormName),
			description: optional(profileFormDescription),
			config_content: profileConfigContent,
			auth_content: profileAuthContent,
		};
		await runAction("profile-create", async () => {
			const result = await track("profile-create", CodexService.CreateProfile(req));
			await Promise.allSettled([refreshDetect(), refreshCodexProfiles(), refreshDashboard(), refreshUsage()]);
			showNotice(t("notice.profileCreated.title"), t("notice.profileCreated.codexDescription", { profile: result.profile.name || result.profile.id }));
			await push(`/codex/profiles/${encodeURIComponent(result.profile.id)}`);
		});
	}

	async function forkCodexProfileFromEditor() {
		const sourceProfileID = currentProfileID;
		const req: ForkCodexProfileRequest = {
			source_profile_id: sourceProfileID,
			profile_id: profileFormID,
			auth_binding: forkAuthBinding,
			name: optional(profileFormName),
			description: optional(profileFormDescription),
		};
		await runAction("profile-fork", async () => {
			const result = await track("profile-fork", CodexService.ForkProfile(req));
			await Promise.allSettled([refreshCodexProfiles(), refreshDashboard()]);
			showNotice(t("notice.profileForked.title"), t("notice.profileForked.codexDescription", { profile: result.profile.name || result.profile.id }));
			await push(`/codex/profiles/${encodeURIComponent(result.profile.id)}`);
		});
	}

	async function syncCodexProfileFromEditor() {
		const req: SyncCodexProfileRequest = {
			profile_id: currentProfileID,
			auth_update: syncAuthUpdate || undefined,
			config_content: profileConfigContent,
			auth_content: profileAuthContent,
		};
		await runAction("profile-sync", async () => {
			const result = await track("profile-sync", CodexService.SyncProfile(req));
			await Promise.allSettled([refreshCodexProfiles(), refreshDashboard()]);
			showNotice(t("notice.profileSynced.title"), t("notice.profileSynced.codexDescription", { profile: result.profile.name || result.profile.id }));
			await loadStoredProfileDraft(result.profile.id);
		});
	}

	function resetProfileEditor() {
		profileEditorError = "";
		profileEditorLoading = false;
		profileDraft = null;
		profileDetail = null;
		profileFormID = "";
		profileFormName = "";
		profileFormDescription = "";
		profileConfigContent = "";
		profileAuthContent = "";
		forkAuthBinding = "share-parent";
		syncAuthUpdate = "";
	}

	async function openUse(profile: AgentProfile) {
		pendingUse = {
			agentID: selectedAgent,
			profileID: profile.id,
			profileName: profile.name,
			plan: null,
		};
		if (selectedAgent !== "codex") {
			useConfirmOpen = true;
			return;
		}
		await runAction("use-build", async () => {
			const plan = await track("use-build", SwitchService.BuildPlan(codexProviderID, profile.id));
			if (pendingUse) pendingUse = { ...pendingUse, plan };
			useConfirmOpen = true;
		});
	}

	async function openUseByProfileID(profileID: string) {
		let summary = codexProfileSummaries.find((item) => item.profile.id === profileID);
		if (!summary) {
			await refreshCodexProfiles();
			summary = codexProfileSummaries.find((item) => item.profile.id === profileID);
		}
		if (summary) await openUse(codexProfileFromSummary(summary));
		else showError(`Codex profile not found: ${profileID}`);
	}

	async function confirmUse() {
		if (!pendingUse) return;
		if (pendingUse.agentID !== "codex") {
			switchPlaceholderProfile(pendingUse.agentID, pendingUse.profileID);
			const agentName = agentByID(pendingUse.agentID).name;
			useConfirmOpen = false;
			showNotice(t("notice.profileSwitched.title"), t("notice.profileSwitched.placeholderDescription", { agent: agentName, profile: pendingUse.profileID }));
			pendingUse = null;
			return;
		}
		if (!pendingUse.plan?.plan_fingerprint) {
			showError(t("errors.profileNotReady"));
			return;
		}
		if (unsupportedOperationCount(pendingUse.plan) > 0) {
			showError(t("errors.unsupportedTargets"));
			return;
		}
		await runAction("use-apply", async () => {
			const current = pendingUse;
			if (!current?.plan) return;
			let result;
			try {
				result = await track(
					"use-apply",
					SwitchService.Apply({
						provider_id: codexProviderID,
						profile_id: current.profileID,
						expected_plan_fingerprint: current.plan.plan_fingerprint,
						confirm: true,
					}),
				);
			} catch (err) {
				if (isErrorCode(err, "TARGET_CHANGED")) {
					await rebuildPendingUsePlan(current);
					showError(t("errors.targetChanged"));
					return;
				}
				throw err;
			}
			useConfirmOpen = false;
			pendingUse = null;
			await Promise.allSettled([refreshCodexProfiles(), refreshDashboard()]);
			showNotice(t("notice.profileSwitched.title"), t("notice.profileSwitched.codexDescription", { profile: result.profile.name || result.profile.id }));
		});
	}

	async function rebuildPendingUsePlan(current: PendingUse) {
		const plan = await track("use-rebuild", SwitchService.BuildPlan(codexProviderID, current.profileID));
		if (pendingUse?.agentID === current.agentID && pendingUse.profileID === current.profileID) {
			pendingUse = { ...pendingUse, plan };
		}
	}

	async function syncUsage() {
		if (selectedAgent !== "codex") {
			showNotice(t("notice.usageSynced.title"), t("notice.usageSynced.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await runAction("usage-sync", async () => {
			usageSyncResult = await track("usage-sync", UsageService.SyncCodex());
			await refreshUsage();
			showNotice(t("notice.usageSynced.title"), t("notice.usageSynced.codexDescription"));
		});
	}

	async function runHealth() {
		if (selectedAgent !== "codex") {
			showNotice(t("notice.healthOK.title"), t("notice.healthOK.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await runAction("doctor", async () => {
			doctorResult = await track("doctor", DoctorService.Run());
			showNotice(t("notice.healthOK.title"), t("notice.healthOK.codexDescription"));
		});
	}

	async function repairLock() {
		if (selectedAgent !== "codex") {
			showNotice(t("notice.lockOK.title"), t("notice.lockOK.noRepair"));
			return;
		}
		await runAction("repair-lock", async () => {
			await track("repair-lock", DoctorService.RepairLock(true));
			showNotice(t("notice.lockOK.title"), t("notice.lockOK.repaired"));
		});
	}

	async function runAction(name: string, fn: () => Promise<void>) {
		if (actionBusy && actionBusy !== name) cancelAction(actionBusy);
		actionBusy = name;
		try {
			await fn();
		} catch (err) {
			if (!isCancelError(err)) showError(err);
		} finally {
			if (actionBusy === name) actionBusy = "";
		}
	}

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		cancelAction(key);
		inFlight.set(key, promise as CancellablePromise<unknown>);
		promise
			.finally(() => {
				if (inFlight.get(key) === promise) inFlight.delete(key);
			})
			.catch(() => {});
		return promise;
	}

	function cancelAction(key: string) {
		inFlight.get(key)?.cancel("replaced");
		inFlight.delete(key);
	}

	function cancelAll() {
		for (const promise of inFlight.values()) promise.cancel("unmount");
		inFlight.clear();
	}

	function handleDashboardUpdate(payload: DashboardUpdatePayload | null | undefined) {
		if (!payload) return;
		if (payload.dashboard) {
			applyDashboardResult(payload.dashboard);
		}
		if (payload.error && !isCancelError(payload.error)) showError(payload.error);
		if (payload.event?.error && !isCancelError(payload.event.error)) showError(payload.event.error);
		void refreshLightForEvent(payload.event);
	}

	async function refreshLightForEvent(event: DesktopChangeEvent | null | undefined) {
		if (!event) return;
		if (event.kind === desktopChangeKind.codexProfileChanged) {
			await refreshDetect();
		}
		updateRefreshSummary();
	}

	function applyDashboardResult(nextDashboard: DashboardResult) {
		dashboard = nextDashboard;
		if (nextDashboard.doctor) doctorResult = nextDashboard.doctor;
		if (nextDashboard.codex_profiles?.profiles) {
			codexProfileSummaries = nextDashboard.codex_profiles.profiles;
			profileError = "";
		}
		if (nextDashboard.usage) {
			usageSummary = nextDashboard.usage;
			usageError = "";
		}
		updateRefreshSummary();
	}

	function buildCodexAgent(): AgentData {
		const profiles = codexProfileSummaries ?? [];
		return {
			id: "codex",
			name: "Codex",
			detected: codexDetected(),
			current: activeCodexProfileID(),
			targets: codexTargets(),
			profiles: profiles.map(codexProfileFromSummary),
			usage: codexUsage(),
			health: codexHealth(),
			placeholder: false,
		};
	}

	function agentByID(agentID: AgentID): AgentData {
		return agentID === "codex" ? codexAgent : placeholderAgents[agentID];
	}

	function activeCodexState() {
		return dashboard?.active_states?.find((state) => state.provider_id === codexProviderID);
	}

	function activeCodexProfileID(): string {
		return activeCodexState()?.profile_id ?? "";
	}

	function activeCodexProfileLabel(): string {
		const state = activeCodexState();
		if (!state?.profile_id) return "";
		if (!state.profile_available) return `missing: ${state.profile_id}`;
		return state.profile_name || state.profile_id;
	}

	function codexDetected(): boolean {
		if (detectResult) {
			return (
				detectResult.codex_dir_exists ||
				detectResult.config_status !== "missing" ||
				detectResult.auth_status !== "missing" ||
				detectResult.provider_exists
			);
		}
		if (codexProfileSummaries.length > 0 || activeCodexState()?.provider_id === codexProviderID) {
			return true;
		}
		return dashboard?.providers?.some((provider) => provider.id === codexProviderID) ?? false;
	}

	function codexTargets(): string[] {
		const targets = [detectResult?.config_path, detectResult?.auth_path].filter(Boolean) as string[];
		if (targets.length > 0) return targets;
		return ["~/.codex/config.toml", "~/.codex/auth.json"];
	}

	function codexProfileFromSummary(summary: CodexProfileSummary): AgentProfile {
		return {
			id: summary.profile.id,
			name: summary.profile.name || summary.profile.id,
			desc: summary.profile.description || t("profile.noDescription"),
			updated: formatRelativeTime(summary.updated_at_unix_ms),
			model: summary.model || "—",
			provider: summary.model_provider || "—",
			baseURL: summary.openai_base_url || "",
			accountID: summary.codex_account_id || "",
			accountLabel: summary.codex_account_id || t("profile.noAccount"),
			summary,
		};
	}

	function codexUsage() {
		return {
			events: usageSummary ? formatInteger(usageSummary.event_count) : "—",
			input: usageSummary ? formatCompact(usageSummary.input_tokens) : "—",
			output: usageSummary ? formatCompact(usageSummary.output_tokens) : "—",
			cost: usageSummary?.estimated_cost_usd ? `$${usageSummary.estimated_cost_usd}` : "—",
		};
	}

	function codexHealth() {
		const findings =
			doctorResult?.findings?.map((finding) => ({
				id: finding.id,
				status: finding.level.toUpperCase(),
				message: finding.message,
			})) ?? [];
		return {
			overall: doctorResult?.overall_level ? doctorResult.overall_level.toUpperCase() : "—",
			lock: doctorResult?.lock?.level ? doctorResult.lock.level.toUpperCase() : "—",
			pending: dashboard?.status.pending_operations ?? 0,
			failed: dashboard?.status.failed_operations ?? 0,
			findings,
		};
	}

	function updateRefreshSummary() {
		const detectStatus = detectResult
			? `${detectResult.config_status}/${detectResult.auth_status}`
			: detectError
				? t("status.error")
				: t("status.pending");
		lastRefreshSummary = t("diagnostics.summary", {
			dashboard: dashboard ? t("status.loaded") : t("status.empty"),
			detect: detectStatus,
			profiles: codexProfileSummaries.length,
		});
	}

	function codexDiagnosticItems(): string[] {
		const items = [lastRefreshSummary];
		if (dashboardError) items.push(t("diagnostics.dashboardError", { message: dashboardError }));
		if (detectError) items.push(t("diagnostics.detectError", { message: detectError }));
		if (profileError) items.push(t("diagnostics.profilesError", { message: profileError }));
		if (usageError) items.push(t("diagnostics.usageError", { message: usageError }));
		return items;
	}

	function showCodexDiagnostics(): boolean {
		if (selectedAgent !== "codex") return false;
		return (
			!currentAgent.detected ||
			currentAgent.profiles.length === 0 ||
			!!dashboardError ||
			!!detectError ||
			!!profileError ||
			!!usageError
		);
	}

	function planWarnings(plan: SwitchPlan | null | undefined): string[] {
		return plan?.warnings ?? [];
	}

	function operationWarnings(operation: PlanOperation): string[] {
		return operation.warnings ?? [];
	}

	function planOperations(plan: SwitchPlan | null | undefined): PlanOperation[] {
		return plan?.operations ?? [];
	}

	function formatPlanAction(value: string): string {
		return value ? value.replace("-", " ") : "unknown";
	}

	function actionVariant(value: string): StatusVariant {
		if (value === "noop") return "ok";
		if (value === "unsupported") return "warn";
		if (value === "create" || value === "update") return "idle";
		return "muted";
	}

	function previewContent(operation: PlanOperation, side: "before" | "after"): string {
		const preview = side === "before" ? operation.before_preview : operation.after_preview;
		if (!preview?.content) return "—";
		return preview.truncated ? `${preview.content}\n[truncated]` : preview.content;
	}

	function savePlaceholderProfile(agentID: AgentID, profileID: string, name: string, description: string) {
		if (agentID === "codex") return;
		const id = profileID.trim() || "work";
		const profile: AgentProfile = {
			id,
			name: name.trim() || id,
			desc: description.trim() || t("profile.savedPlaceholder"),
			updated: t("time.justNow"),
			model: placeholderAgents[agentID].profiles[0]?.model ?? "—",
			provider: placeholderAgents[agentID].profiles[0]?.provider ?? "—",
			baseURL: "",
			accountID: "",
			accountLabel: t("profile.noAccount"),
		};
		updatePlaceholderAgent(agentID, (agent) => ({
			...agent,
			profiles: [profile, ...agent.profiles.filter((item) => item.id !== id)],
		}));
	}

	function switchPlaceholderProfile(agentID: AgentID, profileID: string) {
		if (agentID === "codex") return;
		updatePlaceholderAgent(agentID, (agent) => ({ ...agent, current: profileID }));
	}

	function updatePlaceholderAgent(agentID: PlaceholderAgentID, update: (agent: AgentData) => AgentData) {
		placeholderAgents = { ...placeholderAgents, [agentID]: update(placeholderAgents[agentID]) };
	}

	function toggleTheme() {
		applyTheme(theme === "light" ? "dark" : "light");
	}

	function applyTheme(next: "light" | "dark") {
		theme = next;
		setMode(next);
		document.documentElement.classList.toggle("dark", next === "dark");
	}

	function detectPlatform(): Platform {
		if (typeof navigator === "undefined") return "macos";
		const platformHint =
			(navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ||
			navigator.platform ||
			navigator.userAgent;
		const normalized = platformHint.toLowerCase();
		if (normalized.includes("mac")) return "macos";
		if (normalized.includes("win")) return "windows";
		return "linux";
	}

	function sidebarTopPadding(): string {
		return platform === "macos" ? "pt-[52px]" : "pt-3";
	}

	function unsupportedOperationCount(plan: SwitchPlan | null | undefined): number {
		return (plan?.operations ?? []).filter((operation) => operation.action === "unsupported").length;
	}

	function optional(value: string): string | null {
		const trimmed = value.trim();
		return trimmed === "" ? null : trimmed;
	}

	function formatInteger(value: number): string {
		return new Intl.NumberFormat().format(value);
	}

	function formatCompact(value: number): string {
		if (!value) return "0";
		return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 2 }).format(value);
	}

	function formatDateTime(value: number | undefined): string {
		if (!value) return "—";
		return new Date(value).toLocaleString(undefined, {
			year: "numeric",
			month: "2-digit",
			day: "2-digit",
			hour: "2-digit",
			minute: "2-digit",
			second: "2-digit",
		});
	}

	function formatRelativeTime(value: number | undefined): string {
		if (!value) return "—";
		const now = Date.now();
		const delta = now - value;
		if (delta < 60_000) return t("time.justNow");
		if (delta < 3_600_000) return t("time.minutesAgo", { count: Math.max(1, Math.floor(delta / 60_000)) });
		if (delta < 86_400_000) return t("time.todayAt", { time: new Date(value).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }) });
		return new Date(value).toLocaleDateString(undefined, { month: "short", day: "numeric" });
	}

	function statusPillClass(variant: StatusVariant) {
		return cn(
			"h-auto gap-1 rounded px-1.5 py-px text-xs font-medium leading-none transition-none focus-visible:ring-0",
			variant === "ok" && "border-success/25 bg-success/15 text-success",
			variant === "warn" && "border-warning/25 bg-warning/15 text-warning",
			(variant === "idle" || variant === "muted") && "border-border bg-muted text-muted-foreground",
		);
	}

	function smallButtonClass() {
		return "h-7 rounded-md px-2.5 text-xs";
	}

	function statusVariant(value: string): StatusVariant {
		if (value === "applied" || value === "restored" || value === "OK") return "ok";
		if (value === "failed" || value === "ERROR" || value === "WARN") return "warn";
		return "muted";
	}

	function formatError(value: unknown): string {
		if (!value) return "";
		if (typeof value === "string") return value.trim() === "{}" ? t("errors.desktopUnavailable") : value;
		if (Array.isArray(value)) return value.map(formatError).filter(Boolean).join("\n");
		const typed = value as ErrorShape;
		const cause = errorCause(typed);
		if (cause && cause !== value) {
			const causeMessage = formatError(cause);
			if (causeMessage) return causeMessage;
		}
		const code = errorCode(typed);
		const message = errorMessage(typed);
		if (code && message) return `${code}: ${message}`;
		if (message) return message;
		try {
			const json = JSON.stringify(value);
			return json === "{}" ? t("errors.desktopUnavailable") : json;
		} catch {
			return String(value);
		}
	}

	function showError(value: unknown) {
		const message = formatError(value);
		if (!message || lastToast === message) return;
		lastToast = message;
		toast.error(message);
	}

	function errorCode(value: ErrorShape): string {
		return value.code || value.Code || "";
	}

	function errorMessage(value: ErrorShape): string {
		return value.message || value.Message || "";
	}

	function errorCause(value: ErrorShape): unknown {
		return value.cause ?? value.Cause;
	}

	function showNotice(title: string, description: string) {
		const key = `${title}:${description}`;
		if (lastToast === key) return;
		lastToast = key;
		toast.success(title, { description });
	}

	function isErrorCode(value: unknown, code: string): boolean {
		if (!value) return false;
		if (typeof value === "string") return value.includes(code);
		if (Array.isArray(value)) return value.some((item) => isErrorCode(item, code));
		const typed = value as ErrorShape;
		if (errorCode(typed) === code) return true;
		const cause = errorCause(typed);
		return !!cause && cause !== value && isErrorCode(cause, code);
	}

	function isCancelError(value: unknown): boolean {
		if (!value) return false;
		if (value instanceof CancelError) return true;
		if (Array.isArray(value)) return value.some(isCancelError);
		const typed = value as { name?: string; code?: string; message?: string; cause?: unknown };
		if (typed.name === "CancelError" || typed.code === "CANCELED") return true;
		if (typed.cause && typed.cause !== value && isCancelError(typed.cause)) return true;
		return typeof typed.message === "string" && typed.message.toLowerCase() === "context canceled";
	}

	function t(id: string, values?: Record<string, unknown>): string {
		return String(get(_)(id, values));
	}
</script>

<div class="h-screen overflow-hidden bg-background text-foreground">
	<div class="grid h-full min-h-0 grid-cols-[188px_minmax(0,1fr)]">
		<aside class="drag-region flex min-h-0 flex-col border-r bg-muted/50">
			<div class={cn("px-3 pb-3", sidebarTopPadding())}>
				<div class="flex items-center gap-2 px-1">
					<div class="grid size-5 shrink-0 select-none place-items-center rounded-[6px] bg-foreground text-[10px] font-bold text-background">
						P
					</div>
					<span class="text-sm font-semibold tracking-tight">ProfileDeck</span>
					<span class="ml-auto rounded-full border bg-background/80 px-1.5 py-px text-[10px] text-muted-foreground">
						{$_("app.dev")}
					</span>
				</div>
			</div>

			<nav class="no-drag flex min-h-0 flex-1 flex-col gap-0.5 overflow-auto px-2 pb-2">
				<div class="px-2 pb-1.5 pt-0.5 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
					{$_("nav.agents")}
				</div>
				{#each agentOrder as agentID (agentID)}
					{@const agent = agentByID(agentID)}
					<button
						type="button"
						onclick={() => selectAgent(agentID)}
						class={cn(
							"flex w-full flex-col gap-0.5 rounded-md px-2.5 py-2 text-left transition-colors",
							selectedView !== "settings" && selectedAgent === agentID
								? "bg-background text-foreground shadow-sm ring-1 ring-border"
								: "text-foreground hover:bg-accent",
						)}
					>
						<div class="flex items-center justify-between gap-1">
							<span class="text-sm font-medium">{agent.name}</span>
							<Badge variant="outline" class={statusPillClass(agent.detected ? "ok" : "warn")}>
								{agent.detected ? $_("status.ready") : $_("status.missing")}
							</Badge>
						</div>
						<div class="truncate font-mono text-[11px] text-muted-foreground">
							{agent.current || "—"}
						</div>
					</button>
				{/each}
			</nav>

			<div class="no-drag flex flex-col gap-0.5 border-t px-2 py-2">
				<button
					type="button"
					onclick={() => navigateToView("settings")}
					class={cn(
						"rounded-md px-2.5 py-1.5 text-left text-xs transition-colors hover:bg-accent hover:text-foreground",
						selectedView === "settings" ? "bg-background text-foreground shadow-sm ring-1 ring-border" : "text-muted-foreground",
					)}
				>
					{$_("nav.settings")}
				</button>
			</div>
		</aside>

		<main class="flex min-h-0 min-w-0 flex-col bg-background">
			<div class="drag-region flex h-[50px] shrink-0 items-center justify-between gap-3 border-b px-4">
				<div class="flex min-w-0 items-center gap-2">
					<h1 class="text-base font-bold leading-none tracking-tight">{selectedView === "settings" ? $_("settings.title") : currentAgent.name}</h1>
					{#if selectedView !== "settings"}
						<Badge variant="outline" class={statusPillClass(currentAgent.detected ? "ok" : "warn")}>
							{currentAgent.detected ? $_("status.detected") : $_("status.notDetected")}
						</Badge>
						{#if currentAgent.current}
							<Badge variant="outline" class={statusPillClass("muted")}>{currentAgent.current}</Badge>
						{/if}
					{/if}
				</div>
				<div class="no-drag flex shrink-0 items-center gap-2">
					{#if selectedView !== "settings"}
						<Button class={smallButtonClass()} size="sm" variant="outline" disabled={loading} onclick={refreshSelectedAgent}>
							{#if loading && selectedAgent === "codex"}
								<Spinner data-icon="inline-start" />
							{/if}
							{$_("actions.refresh")}
						</Button>
						<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "detect"} onclick={detectSelectedAgent}>
							{#if actionBusy === "detect"}
								<Spinner data-icon="inline-start" />
							{/if}
							{$_("actions.detect")}
						</Button>
						<Button class={smallButtonClass()} size="sm" onclick={openNewProfile}>{$_("actions.newProfile")}</Button>
						<div class="mx-0.5 h-4 w-px bg-border"></div>
					{/if}
					<button
						type="button"
						onclick={toggleTheme}
						title={theme === "light" ? $_("app.themeToDark") : $_("app.themeToLight")}
						aria-label={theme === "light" ? $_("app.themeToDark") : $_("app.themeToLight")}
						class="flex size-7 items-center justify-center rounded-md text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
					>
						{theme === "light" ? "☾" : "☀"}
					</button>
					</div>
				</div>

				{#if selectedView !== "settings" && showCodexDiagnostics()}
					<div class="no-drag border-b bg-muted/40 px-4 py-2 text-[11px] text-muted-foreground">
						<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
							<span class="font-medium text-foreground">{$_("diagnostics.codexService")}</span>
							{#each codexDiagnosticItems() as item (item)}
								<span class="font-mono">{item}</span>
							{/each}
						</div>
					</div>
				{/if}

				{#if selectedView === "settings"}
					<div class="min-h-0 flex-1 overflow-auto p-4">
						{@render SettingsView()}
					</div>
				{:else}
				<Tabs.Root bind:value={selectedTab} class="flex min-h-0 flex-1 flex-col">
				<div class="shrink-0 border-b px-4">
					<Tabs.List variant="line" class="h-auto gap-0 bg-transparent p-0">
						{#each tabs as tab (tab.id)}
							<Tabs.Trigger
								value={tab.id}
								onclick={() => navigateToView(tab.id)}
								class="h-auto rounded-none border-0 px-3 py-2.5 text-xs font-medium data-active:bg-transparent"
							>
								{$_(tab.labelKey)}
							</Tabs.Trigger>
						{/each}
					</Tabs.List>
				</div>

				<div class="min-h-0 flex-1 overflow-auto p-4">
					<Tabs.Content value="profiles" class="m-0">
						{#if currentProfileRoute === "new"}
							{@render CodexProfileNewView()}
						{:else if currentProfileRoute === "detail"}
							{@render CodexProfileDetailView()}
						{:else if currentProfileRoute === "fork"}
							{@render CodexProfileForkView()}
						{:else}
							<div class="overflow-hidden rounded-lg border bg-card">
								{#if selectedAgent === "codex" && loadingProfiles}
									{@render EmptyState($_("empty.loadingProfilesTitle"), $_("empty.loadingProfilesDescription"))}
								{:else if selectedAgent === "codex" && profileError}
									{@render EmptyState($_("empty.loadProfilesFailedTitle"), profileError)}
								{:else}
									{@render ProfilesView(currentAgent)}
								{/if}
							</div>
						{/if}
					</Tabs.Content>

					<Tabs.Content value="health" class="m-0">
						{@render HealthView(currentAgent)}
					</Tabs.Content>

					<Tabs.Content value="usage" class="m-0">
						{@render UsageView(currentAgent)}
					</Tabs.Content>
				</div>
			</Tabs.Root>
			{/if}
		</main>
	</div>
</div>

<Dialog.Root bind:open={useConfirmOpen}>
	<Dialog.Content class="max-h-[84vh] gap-0 overflow-hidden p-0 sm:max-w-[720px]" showCloseButton={false}>
		<Dialog.Header class="gap-1 px-4 pb-3 pt-4">
			<Dialog.Title class="text-sm font-semibold">
				{t("useDialog.title", {
					profile: pendingUse?.profileName ?? pendingUse?.profileID,
					agent: agentByID(pendingUse?.agentID ?? selectedAgent).name,
				})}
			</Dialog.Title>
			<Dialog.Description class="text-xs leading-relaxed">
				{t("useDialog.description", { agent: agentByID(pendingUse?.agentID ?? selectedAgent).name })}
			</Dialog.Description>
		</Dialog.Header>
		<div class="border-t px-4 pb-2 pt-2.5 font-mono text-xs text-muted-foreground">
			{agentByID(pendingUse?.agentID ?? selectedAgent).current || "—"}
			<span class="mx-1.5 text-muted-foreground/50">→</span>
			{pendingUse?.profileID ?? "—"}
		</div>
		{#if pendingUse?.agentID === "codex"}
			<div class="max-h-[48vh] overflow-auto border-t px-4 py-3">
				{#if planWarnings(pendingUse.plan).length > 0}
					<div class="mb-3 rounded-md border border-warning/25 bg-warning/15 px-2.5 py-2 text-xs text-warning">
						<div class="font-medium">{$_("useDialog.planWarnings")}</div>
						<div class="mt-1 flex flex-col gap-1">
							{#each planWarnings(pendingUse.plan) as warning (warning)}
								<div>{warning}</div>
							{/each}
						</div>
					</div>
				{/if}
				{#if unsupportedOperationCount(pendingUse.plan) > 0}
					<div class="mb-3 rounded-md border border-warning/25 bg-warning/15 px-2.5 py-2 text-xs text-warning">
						{$_("useDialog.unsupported")}
					</div>
				{/if}
				{#if !pendingUse.plan}
					<div class="flex items-center gap-2 text-xs text-muted-foreground">
						<Spinner data-icon="inline-start" />
						{$_("useDialog.building")}
					</div>
				{:else if planOperations(pendingUse.plan).length === 0}
					<div class="text-xs text-muted-foreground">{$_("useDialog.noChanges")}</div>
				{:else}
					<div class="flex flex-col gap-2">
						{#each planOperations(pendingUse.plan) as operation (`${operation.target_id}:${operation.path}`)}
							<div class="rounded-md border bg-card">
								<div class="flex min-w-0 items-center gap-2 border-b px-3 py-2">
									<Badge variant="outline" class={statusPillClass(actionVariant(operation.action))}>
										{formatPlanAction(operation.action)}
									</Badge>
									<span class="font-mono text-xs font-medium">{operation.target_id}</span>
									<span class="truncate font-mono text-xs text-muted-foreground">{operation.path}</span>
								</div>
								<div class="grid gap-0 md:grid-cols-2">
									<div class="min-w-0 border-b p-3 md:border-b-0 md:border-r">
										<div class="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{$_("useDialog.before")}</div>
										<pre class="max-h-32 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2 text-xs">{previewContent(operation, "before")}</pre>
									</div>
									<div class="min-w-0 p-3">
										<div class="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{$_("useDialog.after")}</div>
										<pre class="max-h-32 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2 text-xs">{previewContent(operation, "after")}</pre>
									</div>
								</div>
								{#if operationWarnings(operation).length > 0}
									<div class="border-t px-3 py-2 text-xs text-warning">
										{#each operationWarnings(operation) as warning (warning)}
											<div>{warning}</div>
										{/each}
									</div>
								{/if}
							</div>
						{/each}
					</div>
				{/if}
			</div>
		{/if}
		<Dialog.Footer class="m-0 mx-0 mb-0 rounded-none border-t bg-transparent px-4 py-3">
			<Button class="rounded-md px-3" variant="outline" onclick={() => (useConfirmOpen = false)}>{$_("actions.cancel")}</Button>
			<Button
				class="rounded-md px-3"
				disabled={actionBusy === "use-apply" || (pendingUse?.agentID === "codex" && unsupportedOperationCount(pendingUse.plan) > 0)}
				onclick={confirmUse}
			>
				{#if actionBusy === "use-apply"}
					<Spinner data-icon="inline-start" />
				{/if}
				{$_("actions.useProfile")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

{#snippet EmptyState(title: string, description: string)}
	<Empty.Root class="min-h-0 gap-2 border-0 px-0 py-16">
		<Empty.Header>
			<Empty.Media class="mb-0">
				<span class="text-3xl opacity-40">○</span>
			</Empty.Media>
			<Empty.Title class="text-sm font-medium">{title}</Empty.Title>
			<Empty.Description class="max-w-[220px] text-xs leading-snug">{description}</Empty.Description>
		</Empty.Header>
	</Empty.Root>
{/snippet}

{#snippet SettingsView()}
	<div class="overflow-hidden rounded-lg border bg-card">
		<div class="border-b px-3 py-3">
			<div class="text-sm font-semibold tracking-tight">{$_("settings.title")}</div>
			<div class="mt-1 text-xs text-muted-foreground">{$_("settings.description")}</div>
		</div>
		<Field.FieldGroup class="px-3 py-3">
			<Field.Field>
				<Field.FieldLabel for="desktop-language">{$_("settings.language.label")}</Field.FieldLabel>
				<div class="flex items-center gap-2">
					<select
						id="desktop-language"
						bind:value={languagePreference}
						disabled={languageBusy}
						onchange={(event) => changeLanguage(event.currentTarget.value)}
						class="h-8 rounded-md border border-input bg-background px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50"
					>
						<option value="auto">{$_("settings.language.auto")}</option>
						<option value="zh-CN">{$_("settings.language.zhCN")}</option>
						<option value="en-US">{$_("settings.language.enUS")}</option>
					</select>
					{#if languageBusy}
						<Spinner data-icon="inline-start" />
					{/if}
				</div>
				<Field.FieldDescription>{$_("settings.language.description")}</Field.FieldDescription>
			</Field.Field>
		</Field.FieldGroup>
	</div>
{/snippet}

{#snippet CodexProfileNewView()}
	<div class="flex flex-col gap-3">
		<div class="flex items-center justify-between gap-3">
			<div>
				<div class="text-sm font-semibold tracking-tight">{$_("profilePages.new.title")}</div>
				<div class="mt-1 text-xs text-muted-foreground">{$_("profilePages.new.description")}</div>
			</div>
			<div class="flex shrink-0 items-center gap-2">
				<Button class={smallButtonClass()} size="sm" variant="outline" onclick={backToProfiles}>{$_("actions.back")}</Button>
				<Button class={smallButtonClass()} size="sm" variant="outline" disabled={profileEditorLoading || actionBusy === "profile-create"} onclick={loadCurrentProfileDraft}>
					{#if profileEditorLoading}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.reloadCurrent")}
				</Button>
				<Button class={smallButtonClass()} size="sm" disabled={actionBusy === "profile-create"} onclick={createCodexProfileFromEditor}>
					{#if actionBusy === "profile-create"}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.createProfile")}
				</Button>
			</div>
		</div>
		{@render ProfileEditorCard(false, true)}
	</div>
{/snippet}

{#snippet CodexProfileDetailView()}
	<div class="flex flex-col gap-3">
		<div class="flex items-center justify-between gap-3">
			<div>
				<div class="text-sm font-semibold tracking-tight">
					{t("profilePages.detail.title", { profile: profileDetail?.summary.profile.name || currentProfileID || "—" })}
				</div>
				<div class="mt-1 text-xs text-muted-foreground">{$_("profilePages.detail.description")}</div>
			</div>
			<div class="flex shrink-0 items-center gap-2">
				<Button class={smallButtonClass()} size="sm" variant="outline" onclick={backToProfiles}>{$_("actions.back")}</Button>
				<Button class={smallButtonClass()} size="sm" variant="outline" disabled={profileEditorLoading || actionBusy === "profile-sync"} onclick={loadCurrentProfileDraft}>
					{#if profileEditorLoading}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.reloadCurrent")}
				</Button>
				<Button class={smallButtonClass()} size="sm" disabled={actionBusy === "profile-sync"} onclick={syncCodexProfileFromEditor}>
					{#if actionBusy === "profile-sync"}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.syncProfile")}
				</Button>
			</div>
		</div>
		{#if profileDetail}
			<div class="grid gap-3 md:grid-cols-4">
				{@render Metric($_("profilePages.detail.model"), profileDetail.summary.model || "—")}
				{@render Metric($_("profilePages.detail.provider"), profileDetail.summary.model_provider || "—")}
				{@render Metric($_("profilePages.detail.baseURL"), profileDetail.summary.openai_base_url || "—")}
				{@render Metric($_("profilePages.detail.account"), profileDetail.summary.codex_account_id || $_("profile.noAccount"))}
			</div>
		{/if}
		<div class="overflow-hidden rounded-lg border bg-card">
			<div class="border-b px-3 py-3">
				<div class="text-sm font-semibold tracking-tight">{$_("profilePages.detail.syncOptions")}</div>
				<div class="mt-1 text-xs text-muted-foreground">{$_("profilePages.detail.syncDescription")}</div>
			</div>
			<Field.FieldGroup class="px-3 py-3">
				<Field.Field>
					<Field.FieldLabel for="sync-auth-update">{$_("profilePages.detail.authUpdate")}</Field.FieldLabel>
					<select
						id="sync-auth-update"
						bind:value={syncAuthUpdate}
						class="h-8 w-fit rounded-md border border-input bg-background px-2.5 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
					>
						<option value="">{$_("profilePages.detail.authUpdateDefault")}</option>
						<option value="update-shared">{$_("profilePages.detail.authUpdateShared")}</option>
						<option value="fork-new">{$_("profilePages.detail.authUpdateForkNew")}</option>
					</select>
					<Field.FieldDescription>{$_("profilePages.detail.authUpdateHelp")}</Field.FieldDescription>
				</Field.Field>
			</Field.FieldGroup>
		</div>
		{@render ProfileEditorCard(true, true)}
	</div>
{/snippet}

{#snippet CodexProfileForkView()}
	<div class="flex flex-col gap-3">
		<div class="flex items-center justify-between gap-3">
			<div>
				<div class="text-sm font-semibold tracking-tight">
					{t("profilePages.fork.title", { profile: profileDetail?.summary.profile.name || currentProfileID || "—" })}
				</div>
				<div class="mt-1 text-xs text-muted-foreground">{$_("profilePages.fork.description")}</div>
			</div>
			<div class="flex shrink-0 items-center gap-2">
				<Button class={smallButtonClass()} size="sm" variant="outline" onclick={backToProfiles}>{$_("actions.back")}</Button>
				<Button class={smallButtonClass()} size="sm" disabled={actionBusy === "profile-fork"} onclick={forkCodexProfileFromEditor}>
					{#if actionBusy === "profile-fork"}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.createFork")}
				</Button>
			</div>
		</div>
		<div class="overflow-hidden rounded-lg border bg-card">
			<div class="border-b px-3 py-3">
				<div class="text-sm font-semibold tracking-tight">{$_("profilePages.fork.authBinding")}</div>
				<div class="mt-1 text-xs text-muted-foreground">{$_("profilePages.fork.authBindingDescription")}</div>
			</div>
			<Field.FieldGroup class="px-3 py-3">
				<label class="flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm">
					<input class="mt-0.5" type="radio" bind:group={forkAuthBinding} value="share-parent" />
					<span>
						<span class="block font-medium">{$_("profilePages.fork.shareParent")}</span>
						<span class="block text-xs text-muted-foreground">{$_("profilePages.fork.shareParentDescription")}</span>
					</span>
				</label>
				<label class="flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm">
					<input class="mt-0.5" type="radio" bind:group={forkAuthBinding} value="copy-new" />
					<span>
						<span class="block font-medium">{$_("profilePages.fork.copyNew")}</span>
						<span class="block text-xs text-muted-foreground">{$_("profilePages.fork.copyNewDescription")}</span>
					</span>
				</label>
			</Field.FieldGroup>
		</div>
		{@render ProfileEditorCard(false, false)}
	</div>
{/snippet}

{#snippet ProfileEditorCard(idLocked: boolean, showRawEditors: boolean)}
	<div class="overflow-hidden rounded-lg border bg-card">
		{#if profileEditorLoading}
			{@render EmptyState($_("profilePages.loadingTitle"), $_("profilePages.loadingDescription"))}
		{:else if profileEditorError}
			{@render EmptyState($_("profilePages.errorTitle"), profileEditorError)}
		{:else}
			<div class="border-b px-3 py-3">
				<div class="text-sm font-semibold tracking-tight">{$_("profilePages.form.profile")}</div>
				<div class="mt-1 text-xs text-muted-foreground">{$_("profilePages.form.profileDescription")}</div>
			</div>
			<Field.FieldGroup class="px-3 py-3">
				<Field.Field>
					<Field.FieldLabel for="profile-id">{$_("profilePages.form.profileID")}</Field.FieldLabel>
					<Input id="profile-id" class="rounded-md border-border bg-background text-sm" bind:value={profileFormID} disabled={idLocked} placeholder={$_("profilePages.form.profileIDPlaceholder")} />
				</Field.Field>
				<Field.Field>
					<Field.FieldLabel for="profile-name">{$_("profilePages.form.name")}</Field.FieldLabel>
					<Input id="profile-name" class="rounded-md border-border bg-background text-sm" bind:value={profileFormName} placeholder={$_("profilePages.form.namePlaceholder")} />
				</Field.Field>
				<Field.Field>
					<Field.FieldLabel for="profile-description">{$_("profilePages.form.description")}</Field.FieldLabel>
					<Input id="profile-description" class="rounded-md border-border bg-background text-sm" bind:value={profileFormDescription} placeholder={$_("profilePages.form.descriptionPlaceholder")} />
				</Field.Field>
			</Field.FieldGroup>
			{#if showRawEditors}
				<div class="border-t px-3 py-3">
					<div class="rounded-md border border-warning/25 bg-warning/10 px-2.5 py-2 text-xs text-warning">
						{$_("profilePages.rawAuthWarning")}
					</div>
				</div>
				<Field.FieldGroup class="px-3 pb-3">
					<Field.Field>
						<Field.FieldLabel for="profile-config-content">{$_("profilePages.form.config")}</Field.FieldLabel>
						<textarea
							id="profile-config-content"
							class="min-h-48 w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-xs outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
							bind:value={profileConfigContent}
							spellcheck="false"
						></textarea>
						<Field.FieldDescription>{profileDraft?.config_path || "config.toml"}</Field.FieldDescription>
					</Field.Field>
					<Field.Field>
						<Field.FieldLabel for="profile-auth-content">{$_("profilePages.form.auth")}</Field.FieldLabel>
						<textarea
							id="profile-auth-content"
							class="min-h-40 w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-xs outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
							bind:value={profileAuthContent}
							spellcheck="false"
						></textarea>
						<Field.FieldDescription>{profileDraft?.auth_path || "auth.json"}</Field.FieldDescription>
					</Field.Field>
				</Field.FieldGroup>
			{/if}
		{/if}
	</div>
{/snippet}

{#snippet ProfilesView(agent: AgentData)}
	{#if !agent.detected}
		{@render EmptyState($_("empty.agentNotDetectedTitle"), t("empty.agentNotDetectedDescription", { agent: agent.name }))}
	{:else if agent.profiles.length === 0}
		{@render EmptyState($_("empty.noProfilesTitle"), t("empty.noProfilesDescription", { agent: agent.name }))}
	{:else}
		{#each agent.profiles as profile, index (profile.id)}
			{@const active = profile.id === agent.current}
			<div
				class={cn(
					"grid grid-cols-[1fr_auto] items-center gap-4 px-3 py-3 transition-colors",
					index < agent.profiles.length - 1 && "border-b",
					active ? "bg-accent/50" : "hover:bg-muted/60",
				)}
			>
				<div class="flex min-w-0 flex-col gap-1">
					<div class="flex items-center gap-2">
						{#if active}
							<span class="size-1.5 shrink-0 rounded-full bg-success"></span>
						{/if}
						<span class="truncate text-sm font-semibold tracking-tight">{profile.name}</span>
						{#if active}
							<Badge variant="outline" class={statusPillClass("ok")}>{$_("status.active")}</Badge>
						{/if}
					</div>
					<div class="truncate text-xs text-muted-foreground">{profile.desc}</div>
					<div class="truncate font-mono text-xs text-muted-foreground">
						{profile.provider} · {profile.model}{profile.baseURL ? ` · ${profile.baseURL}` : ""} · {profile.accountLabel} · {profile.updated}
					</div>
				</div>
				<div class="flex shrink-0 items-center gap-1.5">
					<Button class={smallButtonClass()} size="sm" disabled={!!actionBusy || active} onclick={() => openUse(profile)}>{$_("actions.useProfile")}</Button>
					<Button class={smallButtonClass()} size="sm" variant="outline" onclick={() => openForkProfile(profile)}>{$_("actions.fork")}</Button>
					<Button class={smallButtonClass()} size="sm" variant="outline" onclick={() => openProfileDetails(profile)}>{$_("actions.details")}</Button>
				</div>
			</div>
		{/each}
	{/if}
{/snippet}

{#snippet HealthView(agent: AgentData)}
	<div class="flex flex-col gap-3">
		<div class="grid grid-cols-4 gap-3">
			{@render Metric($_("health.overall"), agent.health.overall)}
			{@render Metric($_("health.lock"), agent.health.lock)}
			{@render Metric($_("health.pending"), String(agent.health.pending))}
			{@render Metric($_("health.failed"), String(agent.health.failed))}
		</div>
		<div class="overflow-hidden rounded-lg border bg-card">
			<div class="flex items-center justify-end gap-2 border-b px-3 py-2">
				<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "doctor"} onclick={runHealth}>
					{#if actionBusy === "doctor"}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.checkHealth")}
				</Button>
				<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "repair-lock"} onclick={repairLock}>
					{#if actionBusy === "repair-lock"}
						<Spinner data-icon="inline-start" />
					{/if}
					{$_("actions.repairLock")}
				</Button>
			</div>
			{#if agent.health.findings.length === 0 && agent.health.pending === 0 && agent.health.failed === 0}
				<Empty.Root class="min-h-0 gap-2 border-0 px-0 py-16">
					<Empty.Header>
						<Empty.Media class="mb-0">
							<span class="text-3xl opacity-60">✓</span>
						</Empty.Media>
						<Empty.Title class="text-sm font-medium">{$_("empty.allChecksPassedTitle")}</Empty.Title>
						<Empty.Description class="text-xs leading-snug">{$_("empty.allChecksPassedDescription")}</Empty.Description>
					</Empty.Header>
				</Empty.Root>
			{:else}
				<Table.Root>
					<Table.Header>
						<Table.Row>
							<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{$_("health.finding")}</Table.Head>
							<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{$_("health.status")}</Table.Head>
							<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{$_("health.message")}</Table.Head>
						</Table.Row>
					</Table.Header>
					<Table.Body>
						{#each agent.health.findings as finding (finding.id)}
							<Table.Row class="last:border-0">
								<Table.Cell class="px-3 py-2.5 font-mono text-xs">{finding.id}</Table.Cell>
								<Table.Cell class="px-3 py-2.5">
									<Badge variant="outline" class={statusPillClass(statusVariant(finding.status))}>{finding.status}</Badge>
								</Table.Cell>
								<Table.Cell class="px-3 py-2.5 text-xs text-muted-foreground">{finding.message}</Table.Cell>
							</Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet UsageView(agent: AgentData)}
	<div class="flex flex-col gap-3">
		<div class="flex justify-end">
			<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "usage-sync"} onclick={syncUsage}>
				{#if actionBusy === "usage-sync"}
					<Spinner data-icon="inline-start" />
				{/if}
				{$_("actions.sync")}
			</Button>
		</div>
		<div class="grid grid-cols-2 gap-3">
			{@render Metric($_("usage.events"), agent.usage.events)}
			{@render Metric($_("usage.inputTokens"), agent.usage.input)}
			{@render Metric($_("usage.outputTokens"), agent.usage.output)}
			{@render Metric($_("usage.cost"), agent.usage.cost, agent.usage.cost === "—" ? $_("usage.configurePricing") : undefined)}
		</div>
		{#if selectedAgent === "codex" && usageSyncResult?.errors?.length}
			<div class="rounded-lg border bg-card px-3 py-2 text-xs text-muted-foreground">
				{t("usage.importErrors", { count: usageSyncResult.errors.length })}
			</div>
		{/if}
	</div>
{/snippet}

{#snippet Metric(label: string, value: string, sub?: string)}
	<div class="flex min-w-0 flex-col gap-1 rounded-lg border bg-card p-3">
		<div class="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
		<div class="truncate text-xl font-bold tracking-tight">{value}</div>
		{#if sub}
			<div class="text-xs text-muted-foreground">{sub}</div>
		{/if}
	</div>
{/snippet}
