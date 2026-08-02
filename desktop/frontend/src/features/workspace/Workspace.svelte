<script lang="ts">
	import { onMount, untrack } from "svelte";
	import { Events, type CancellablePromise } from "@wailsio/runtime";
	import { setMode } from "mode-watcher";
	import { push, replace, router } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import BotIcon from "@lucide/svelte/icons/bot";
	import CircleArrowUpIcon from "@lucide/svelte/icons/circle-arrow-up";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import OrbitIcon from "@lucide/svelte/icons/orbit";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import SettingsIcon from "@lucide/svelte/icons/settings";
	import SparklesIcon from "@lucide/svelte/icons/sparkles";
	import StethoscopeIcon from "@lucide/svelte/icons/stethoscope";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import {
		AppService,
		AgentService,
		AntigravityService,
		ClaudeCodeService,
		CodexService,
		DoctorService,
		GrokBuildService,
		SettingsService,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { DashboardResult, DesktopError } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import { Service as UpdateService, type UpdateStatus } from "../../../bindings/github.com/strahe/profiledeck/desktop/update";
	import type { AntigravityDetectResult, AntigravityProfileSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/antigravity/models";
	import type { ClaudeCodeDetectResult, ClaudeCodeProfileSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/claudecode/models";
	import type { CodexConfigSet, CodexDetectResult, CodexProfileSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/codex/models";
	import type {
		ConfigSet as GrokBuildConfigSet,
		DetectResult as GrokBuildDetectResult,
		ProfileSummary as GrokBuildProfileSummary,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
	import type { DoctorResult } from "../../../bindings/github.com/strahe/profiledeck/internal/doctor/models";
	import { darkThemeAppIconURL, lightThemeAppIconURL } from "$lib/app-icon";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import { Separator } from "$lib/components/ui/separator";
	import * as Sidebar from "$lib/components/ui/sidebar";
	import { Spinner } from "$lib/components/ui/spinner";
	import * as Tabs from "$lib/components/ui/tabs";
	import * as Tooltip from "$lib/components/ui/tooltip";
	import { desktopErrorMessage, isCancelError } from "$lib/desktop-errors";
	import {
		applyDesktopLanguagePreference,
		currentDesktopLocale,
		normalizeDesktopLanguage,
		translate,
		type DesktopLanguage,
	} from "$lib/i18n";
	import { AntigravityQuotaReadPolicy } from "../profiles/antigravity-quota-policy.js";
	import { AntigravityQuotaController } from "../profiles/antigravity-quota.svelte.js";
	import { CodexStartupQuotaReadCoordinator } from "../profiles/codex-quota-policy.js";
	import {
		GrokBuildQuotaReadPolicy,
		reconcileGrokBuildProfileSummaries,
	} from "../profiles/grok-build-quota-policy.js";
	import { GrokBuildQuotaController } from "../profiles/grok-build-quota.svelte.js";
	import type { ProfileUseRequest } from "../profiles/types";
	import { provideCodexRuntime } from "../settings/codex-runtime.svelte.js";
	import WorkspaceViewStatus from "./WorkspaceViewStatus.svelte";
	import { selectLatestUpdateStatus } from "./update-status-policy.js";
	import {
		agentForWorkspace,
		agentHome,
		isAgentWorkspace,
		parseWorkspaceRoute,
		type AgentID,
		type WorkspaceView,
	} from "./workspace-route-policy";

	type Appearance = "system" | "light" | "dark";
	type Platform = "macos" | "windows" | "linux";
	type GlobalSettingsTab = "general" | "backups";

	type DesktopChangeEvent = {
		kind: string;
		status?: string;
		error?: DesktopError | null;
		profile_id?: string;
		operation_id?: string;
		profile_changed?: boolean;
		config_sets_changed?: boolean;
		active_state_changed?: boolean;
		provider_id?: string;
		agent_id?: string;
		agent_enabled?: boolean;
	};

	type DashboardUpdatePayload = {
		event: DesktopChangeEvent;
		events?: DesktopChangeEvent[];
		dashboard: DashboardResult;
		error?: DesktopError | null;
	};

	const codexProviderID = "codex";
	const grokBuildProviderID = "grok-build";
	const antigravityProviderID = "antigravity";
	const claudeCodeProviderID = "claude-code";
	const agents: Array<{ id: AgentID; name: string; icon: typeof BotIcon }> = [
		{ id: "codex", name: "Codex", icon: BotIcon },
		{ id: "grok-build", name: "Grok Build", icon: SparklesIcon },
		{ id: "antigravity", name: "Antigravity", icon: OrbitIcon },
		{ id: "claude-code", name: "Claude Code", icon: BotIcon },
	];
	const loadCodexProfiles = () => import("../profiles/CodexProfiles.svelte");
	const loadGrokBuildProfiles = () => import("../profiles/GrokBuildProfiles.svelte");
	const loadAntigravityProfiles = () => import("../profiles/AntigravityProfiles.svelte");
	const loadClaudeCodeProfiles = () => import("../profiles/ClaudeCodeProfiles.svelte");
	const loadUsagePage = () => import("../usage/UsagePage.svelte");
	const loadCodexSettings = () => import("../settings/CodexSettings.svelte");
	const loadGrokBuildSettings = () => import("../settings/GrokBuildSettings.svelte");
	const loadGlobalSettings = () => import("./GlobalSettings.svelte");
	const loadDiagnosticsPage = () => import("./DiagnosticsPage.svelte");
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let platform = $state<Platform>(detectPlatform());
	let loading = $state(false);
	let loadingProfiles = $state(true);
	let loadingGrokBuildProfiles = $state(true);
	let loadingAntigravityProfiles = $state(true);
	let loadingClaudeCodeProfiles = $state(true);
	let actionBusy = $state("");
	let languageBusy = $state(false);
	let appearanceBusy = $state(false);
	let agentBusy = $state("");
	let updateBusy = $state("");
	let sidebarBusy = $state(false);
	let languagePreference = $state<DesktopLanguage>("auto");
	let appearance = $state<Appearance>("system");
	let persistedAppearance = $state<Appearance>("system");
	let sidebarOpen = $state(true);
	let persistedSidebarCollapsed = $state(false);
	let settingsLoaded = $state(false);
	let automaticBackups = $state(true);
	let globalSettingsTab = $state<GlobalSettingsTab>("general");
	let contentViewport = $state<HTMLDivElement | null>(null);
	let startupRecoveryRedirected = false;
	let lastToast = "";
	let invalidRoute = "";
	let currentPath = $state(router.location);
	let updateStatus = $state<UpdateStatus>({
		revision: 0,
		management: "unavailable",
		configured: false,
		automatic: false,
		channel: "",
		state: "unavailable",
		current_version: "dev",
		available_version: "",
		downloaded_bytes: 0,
		total_bytes: 0,
		last_checked_at_unix_ms: 0,
		error_code: "",
	});

	let dashboard = $state<DashboardResult | null>(null);
	let detectResult = $state<CodexDetectResult | null>(null);
	let grokBuildDetectResult = $state<GrokBuildDetectResult | null>(null);
	let antigravityDetectResult = $state<AntigravityDetectResult | null>(null);
	let claudeCodeDetectResult = $state<ClaudeCodeDetectResult | null>(null);
	let doctorResult = $state<DoctorResult | null>(null);
	let codexProfileSummaries = $state<CodexProfileSummary[]>([]);
	let grokBuildProfileSummaries = $state<GrokBuildProfileSummary[]>([]);
	let antigravityProfileSummaries = $state<AntigravityProfileSummary[]>([]);
	let claudeCodeProfileSummaries = $state<ClaudeCodeProfileSummary[]>([]);
	let codexConfigSets = $state<CodexConfigSet[]>([]);
	let grokBuildConfigSets = $state<GrokBuildConfigSet[]>([]);
	let dashboardError = $state("");
	let detectError = $state("");
	let grokBuildDetectError = $state("");
	let antigravityDetectError = $state("");
	let claudeCodeDetectError = $state("");
	let doctorError = $state("");
	let profileError = $state("");
	let grokBuildProfileError = $state("");
	let antigravityProfileError = $state("");
	let claudeCodeProfileError = $state("");
	let useRequest = $state<ProfileUseRequest | null>(null);
	let grokBuildUseRequest = $state<ProfileUseRequest | null>(null);
	let antigravityUseRequest = $state<ProfileUseRequest | null>(null);
	let claudeCodeUseRequest = $state<ProfileUseRequest | null>(null);
	let useRequestSequence = 0;

	const codexRuntime = provideCodexRuntime({ showError, showNotice });
	const codexStartupQuotaRead = new CodexStartupQuotaReadCoordinator();
	const grokBuildQuota = new GrokBuildQuotaController({ showError });
	const grokBuildQuotaReadPolicy = new GrokBuildQuotaReadPolicy();
	const antigravityQuota = new AntigravityQuotaController({ showError });
	const antigravityQuotaReadPolicy = new AntigravityQuotaReadPolicy();
	let workspaceRoute = $derived(parseWorkspaceRoute(currentPath));
	let agentWorkspace = $derived(isAgentWorkspace(workspaceRoute.view));
	let selectedAgent = $derived<AgentID | null>(agentForWorkspace(workspaceRoute.view));
	let agentWorkspaceReady = $derived(!agentWorkspace || (dashboard !== null && dashboard.status.initialized && dashboard.status.schema_healthy && selectedAgent !== null && isAgentEnabled(selectedAgent)));
	let enabledAgents = $derived.by(() => {
		const states = dashboard?.agents;
		if (!states?.length) return agents;
		const enabled = new Set<string>(states.filter((state) => state.enabled).map((state) => String(state.manifest.id)));
		return agents.filter((agent) => enabled.has(agent.id));
	});
	let activeAgentTab = $derived(workspaceRoute.view === "codex-settings" || workspaceRoute.view === "grok-build-settings"
		? "settings"
		: workspaceRoute.view === "grok-build-usage"
			? "usage"
		: workspaceRoute.view === "antigravity-profiles"
			|| workspaceRoute.view === "claude-code-profiles"
			|| workspaceRoute.view === "grok-build-profiles"
			? "profiles"
			: workspaceRoute.view);
	let codexActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === codexProviderID)?.profile_id ?? "");
	let grokBuildActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === grokBuildProviderID)?.profile_id ?? "");
	let antigravityActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === antigravityProviderID)?.profile_id ?? "");
	let claudeCodeActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === claudeCodeProviderID)?.profile_id ?? "");
	let codexProviderExists = $derived(dashboard?.providers?.some((provider) => provider.id === codexProviderID) ?? false);
	let grokBuildProviderExists = $derived(
		dashboard?.providers?.some((provider) => provider.id === grokBuildProviderID)
			?? grokBuildDetectResult?.provider_exists
			?? false,
	);
	let usageProviderID = $derived(workspaceRoute.view === "grok-build-usage" ? grokBuildProviderID : codexProviderID);
	let usageProviderName = $derived(workspaceRoute.view === "grok-build-usage" ? "Grok Build" : "Codex");
	let usageProviderExists = $derived(workspaceRoute.view === "grok-build-usage" ? grokBuildProviderExists : codexProviderExists);
	let activeProfileID = $derived(
		selectedAgent === "grok-build"
			? grokBuildActiveProfileID
			: selectedAgent === "antigravity"
				? antigravityActiveProfileID
				: selectedAgent === "claude-code"
					? claudeCodeActiveProfileID
					: codexActiveProfileID,
	);
	let currentProfileName = $derived.by(() => {
		void $locale;
		const active = selectedAgent === "grok-build"
			? grokBuildProfileSummaries.find((summary) => summary.profile.id === activeProfileID)
			: selectedAgent === "antigravity"
				? antigravityProfileSummaries.find((summary) => summary.profile.id === activeProfileID)
				: selectedAgent === "claude-code"
					? claudeCodeProfileSummaries.find((summary) => summary.profile.id === activeProfileID)
					: codexProfileSummaries.find((summary) => summary.profile.id === activeProfileID);
		if (active?.profile.name) return active.profile.name;
		const id = active?.profile.id || activeProfileID;
		return id ? `${translate("profile.unnamed")} · ${shortID(id)}` : "";
	});
	let pageTitle = $derived.by(() => {
		void $locale;
		switch (workspaceRoute.view) {
			case "settings": return translate("settings.title");
			case "diagnostics": return translate("diagnosticsPage.title");
			default: return selectedAgent === "grok-build"
				? "Grok Build"
				: selectedAgent === "antigravity"
					? "Antigravity"
					: selectedAgent === "claude-code" ? "Claude Code" : "Codex";
		}
	});
	let titlebarOffset = $derived(sidebarOpen ? "10rem" : platform === "macos" ? "5rem" : "3rem");
	let sidebarUpdate = $derived.by(() => {
		void $locale;
		const version = updateStatus.available_version;
		if (updateStatus.state === "downloading") {
			const percent = updateStatus.total_bytes > 0
				? Math.round(Math.min(100, Math.max(0, (updateStatus.downloaded_bytes / updateStatus.total_bytes) * 100)))
				: 0;
			return {
				visible: true,
				ready: false,
				label: translate("settings.updates.sidebar.downloading", { value: percent }),
				accessibleLabel: translate("settings.updates.sidebar.downloadingDescription", { version, value: percent }),
			};
		}
		if (updateStatus.state === "verifying" || updateStatus.state === "preparing") {
			return {
				visible: true,
				ready: false,
				label: translate("settings.updates.sidebar.preparing"),
				accessibleLabel: translate("settings.updates.sidebar.preparingDescription", { version }),
			};
		}
		if (updateStatus.state === "ready") {
			return {
				visible: true,
				ready: true,
				label: translate("settings.updates.sidebar.restart"),
				accessibleLabel: translate("settings.updates.sidebar.restartDescription", { version }),
			};
		}
		return { visible: false, ready: false, label: "", accessibleLabel: "" };
	});

	$effect(() => {
		const profiles = codexProfileSummaries;
		untrack(() => codexRuntime.setProfiles(profiles));
	});

	$effect(() => {
		const profiles = grokBuildProfileSummaries;
		const activeProfileID = grokBuildActiveProfileID;
		untrack(() => grokBuildQuota.setProfiles(profiles, activeProfileID));
	});

	$effect(() => {
		const profiles = antigravityProfileSummaries;
		untrack(() => antigravityQuota.setProfiles(profiles));
	});

	$effect(() => {
		const path = currentPath;
		const available = enabledAgents;
		if (path === "/codex/health") {
			void replace("/diagnostics");
			return;
		}
		const routeAgent = agentForWorkspace(workspaceRoute.view);
		if (workspaceRoute.valid && (!routeAgent || available.some((agent) => agent.id === routeAgent))) {
			invalidRoute = "";
		} else if (invalidRoute !== path) {
			invalidRoute = path;
			void replace(available.length ? agentHome(available[0].id) : "/settings");
		}
	});

	onMount(() => {
		const syncPath = () => {
			currentPath = router.location;
		};
		window.addEventListener("hashchange", syncPath);
		syncPath();
		void loadSettings();
		const stopRuntime = codexRuntime.start(false);
		void refreshAll();
		const off = [
			Events.On("profiledeck:open-switch", (event) => {
				const payload = event.data ?? {};
				if (!payload.profile_id) return;
				if (payload.provider_id === codexProviderID && isAgentEnabled("codex")) {
					useRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
					void push("/codex/profiles");
				} else if (payload.provider_id === grokBuildProviderID && isAgentEnabled("grok-build")) {
					grokBuildUseRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
					void push("/grok-build/profiles");
				} else if (payload.provider_id === antigravityProviderID && isAgentEnabled("antigravity")) {
					antigravityUseRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
					void push("/antigravity/profiles");
				} else if (payload.provider_id === claudeCodeProviderID && isAgentEnabled("claude-code")) {
					claudeCodeUseRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
					void push("/claude-code/profiles");
				}
			}),
			Events.On("profiledeck:open-doctor", () => {
				void push("/diagnostics");
				void runDoctor();
			}),
			Events.On("profiledeck:dashboard-updated", (event) => handleDashboardUpdate(event.data as DashboardUpdatePayload)),
			Events.On("profiledeck:operation-error", (event) => {
				if (!isCancelError(event.data)) showError(event.data);
			}),
			Events.On("profiledeck:update-status", (event) => {
				applyUpdateStatus(event.data as UpdateStatus);
			}),
		];
		// Subscribe before the initial snapshot so a fast background download
		// cannot become ready in the gap between the status read and listener.
		void loadUpdateStatus();

		return () => {
			window.removeEventListener("hashchange", syncPath);
			for (const dispose of off) dispose();
			stopRuntime();
			grokBuildQuota.stop();
			antigravityQuota.stop();
			cancelAll();
		};
	});

	async function refreshAll(reloadRuntime = true) {
		loading = true;
		try {
			const dashboardResult = await track("dashboard", AppService.Dashboard());
			applyDashboardResult(dashboardResult);
			codexRuntime.setProfiles(codexProfileSummaries);
			const grokBuildCurrentProfileID = dashboardActiveProfileID(dashboardResult, grokBuildProviderID);
			grokBuildQuota.setProfiles(grokBuildProfileSummaries, grokBuildCurrentProfileID);
			antigravityQuota.setProfiles(antigravityProfileSummaries);
			const grokBuildStartupProfileID = grokBuildQuotaReadPolicy.startup(
				grokBuildCurrentProfileID,
				agentEnabled(dashboardResult.agents ?? [], "grok-build"),
			);
			if (grokBuildStartupProfileID) void grokBuildQuota.readCurrentQuota(grokBuildStartupProfileID);
			const startupProfileID = antigravityQuotaReadPolicy.startup(
				dashboardResult.active_states?.find((state) => state.provider_id === antigravityProviderID)?.profile_id ?? "",
			);
			if (startupProfileID) void antigravityQuota.readQuota(startupProfileID);
			dashboardError = "";

			const tasks: Promise<unknown>[] = [];
			const routeAgent = agentForWorkspace(workspaceRoute.view);
			if (routeAgent && isAgentEnabled(routeAgent)) {
				tasks.push(
					routeAgent === "grok-build"
						? refreshGrokBuildDetect()
						: routeAgent === "antigravity"
							? refreshAntigravityDetect()
							: routeAgent === "claude-code" ? refreshClaudeCodeDetect() : refreshDetect(),
				);
			}
			if (reloadRuntime && isAgentEnabled("codex")) {
				const runtimeReady = codexRuntime.load();
				tasks.push(runtimeReady);
				tasks.push(codexStartupQuotaRead.start(
					dashboardResult.active_states?.find((state) => state.provider_id === codexProviderID)?.profile_id ?? "",
					runtimeReady,
					(profileID) => { void codexRuntime.readQuota(profileID); },
				));
			} else if (!isAgentEnabled("codex")) {
				tasks.push(codexStartupQuotaRead.start("", Promise.resolve(), () => {}));
			}
			await Promise.all(tasks);
		} catch (error) {
			if (!isCancelError(error)) {
				dashboardError = formatError(error);
				showError(error);
			}
			loadingProfiles = false;
			loadingGrokBuildProfiles = false;
			loadingAntigravityProfiles = false;
			loadingClaudeCodeProfiles = false;
		} finally {
			loading = false;
		}
	}

	async function loadSettings() {
		try {
			const settings = await track("settings", SettingsService.Get());
			languagePreference = applyLanguageAndNotifyTray(settings.language);
			appearance = normalizeAppearance(settings.appearance);
			persistedAppearance = appearance;
			setMode(appearance);
			persistedSidebarCollapsed = settings.sidebar_collapsed;
			sidebarOpen = !settings.sidebar_collapsed;
			automaticBackups = settings.automatic_backups;
			settingsLoaded = true;
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		}
	}

	async function loadUpdateStatus() {
		try {
			applyUpdateStatus(await track("update-status", UpdateService.Status()));
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		}
	}

	async function changeAutomaticUpdates(enabled: boolean) {
		if (updateBusy) return;
		updateBusy = "automatic";
		try {
			const next = await track("update-automatic", UpdateService.SetAutomatic(enabled));
			applyUpdateStatus(next);
			if (next.error_code === "settings_unavailable") {
				toast.error(translate("settings.updates.error.settingsUnavailable"));
			}
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			updateBusy = "";
		}
	}

	async function changeUpdateChannel(channel: string) {
		if (updateBusy || channel === updateStatus.channel) return;
		updateBusy = "channel";
		try {
			const next = await track("update-channel", UpdateService.SetChannel(channel));
			applyUpdateStatus(next);
			if (next.error_code === "settings_unavailable") {
				toast.error(translate("settings.updates.error.settingsUnavailable"));
			}
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			updateBusy = "";
		}
	}

	async function checkForUpdates() {
		if (updateBusy) return;
		updateBusy = "check";
		try {
			const next = await track("update-check", UpdateService.CheckAndDownload());
			applyUpdateStatus(next);
			if (next.state === "error") {
				toast.error(updateFailureMessage(next.error_code));
			} else if (next.state === "up_to_date") {
				showNotice(translate("settings.updates.upToDateTitle"), translate("settings.updates.state.upToDate"));
			}
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			updateBusy = "";
		}
	}

	async function restartWithUpdate() {
		if (updateBusy) return;
		updateBusy = "restart";
		try {
			await track("update-restart", UpdateService.Restart());
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			updateBusy = "";
		}
	}

	function updateFailureMessage(code: string): string {
		switch (code) {
			case "feed_unavailable": return translate("settings.updates.error.unavailable");
			case "feed_invalid":
				return translate("settings.updates.error.feedRejected");
			case "artifact_verification_failed": return translate("settings.updates.error.artifactRejected");
			default: return translate("settings.updates.error.generic");
		}
	}

	function applyUpdateStatus(next: UpdateStatus) {
		updateStatus = selectLatestUpdateStatus(updateStatus, next);
	}

	async function changeLanguage(value: string) {
		const next = normalizeDesktopLanguage(value);
		if (next === languagePreference || languageBusy) return;
		const previous = languagePreference;
		languagePreference = applyLanguageAndNotifyTray(next);
		languageBusy = true;
		try {
			const settings = await track("settings-language", SettingsService.Update({ language: next }));
			languagePreference = applyLanguageAndNotifyTray(settings.language);
			showNotice(translate("notice.settingsSaved.title"), translate("notice.settingsSaved.description"));
		} catch (error) {
			languagePreference = applyLanguageAndNotifyTray(previous);
			if (!isCancelError(error)) showError(error);
		} finally {
			languageBusy = false;
		}
	}

	function applyLanguageAndNotifyTray(value: string | undefined | null): DesktopLanguage {
		const preference = applyDesktopLanguagePreference(value);
		void Events.Emit("profiledeck:locale-changed", currentDesktopLocale());
		return preference;
	}

	async function changeAppearance(value: string) {
		const next = normalizeAppearance(value);
		if (next === appearance || appearanceBusy) return;
		const previous = persistedAppearance;
		appearance = next;
		setMode(next);
		appearanceBusy = true;
		try {
			const settings = await track("settings-appearance", SettingsService.Update({ appearance: next }));
			appearance = normalizeAppearance(settings.appearance);
			persistedAppearance = appearance;
			setMode(appearance);
			showNotice(translate("notice.settingsSaved.title"), translate("notice.settingsSaved.description"));
		} catch (error) {
			appearance = previous;
			setMode(previous);
			if (!isCancelError(error)) showError(error);
		} finally {
			appearanceBusy = false;
		}
	}

	async function changeAgentEnabled(agentID: string, enabled: boolean) {
		if (agentBusy) return;
		agentBusy = agentID;
		try {
			await track(`agent:${agentID}`, AgentService.SetEnabled(agentID, enabled));
			await refreshAll(false);
			showNotice(
				translate("settings.agents.savedTitle"),
				translate(enabled ? "settings.agents.enabledDescription" : "settings.agents.disabledDescription", { agent: agentName(agentID) }),
			);
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			agentBusy = "";
		}
	}

	async function saveSidebarState(open: boolean) {
		if (!settingsLoaded) return;
		if (sidebarBusy) {
			sidebarOpen = !persistedSidebarCollapsed;
			return;
		}
		sidebarBusy = true;
		try {
			const settings = await SettingsService.Update({ sidebar_collapsed: !open });
			persistedSidebarCollapsed = settings.sidebar_collapsed;
			sidebarOpen = !settings.sidebar_collapsed;
		} catch (error) {
			sidebarOpen = !persistedSidebarCollapsed;
			if (!isCancelError(error)) showError(error);
		} finally {
			sidebarBusy = false;
		}
	}

	async function refreshDetect() {
		try {
			const result = await track("detect", CodexService.Detect());
			detectResult = result;
			detectError = "";
			return result;
		} catch (error) {
			if (!isCancelError(error)) {
				detectResult = null;
				detectError = formatError(error);
				showError(error);
			}
			return null;
		}
	}

	async function refreshCodexProfiles() {
		loadingProfiles = true;
		try {
			const result = await track("codex-profiles", CodexService.ListProfiles());
			codexProfileSummaries = result.profiles ?? [];
			profileError = "";
		} catch (error) {
			if (!isCancelError(error)) profileError = formatError(error);
		} finally {
			loadingProfiles = false;
		}
	}

	async function refreshGrokBuildDetect() {
		try {
			const result = await track("grok-build-detect", GrokBuildService.Detect());
			grokBuildDetectResult = result;
			grokBuildDetectError = "";
			return result;
		} catch (error) {
			if (!isCancelError(error)) {
				grokBuildDetectResult = null;
				grokBuildDetectError = formatError(error);
			}
			return null;
		}
	}

	async function refreshGrokBuildProfiles() {
		loadingGrokBuildProfiles = true;
		try {
			const result = await track("grok-build-profiles", GrokBuildService.ListProfiles());
			grokBuildProfileSummaries = result.profiles ?? [];
			grokBuildProfileError = "";
		} catch (error) {
			if (!isCancelError(error)) grokBuildProfileError = formatError(error);
		} finally {
			loadingGrokBuildProfiles = false;
		}
	}

	async function refreshAntigravityDetect() {
		try {
			const result = await track("antigravity-detect", AntigravityService.Detect());
			antigravityDetectResult = result;
			antigravityDetectError = "";
			return result;
		} catch (error) {
			if (!isCancelError(error)) {
				antigravityDetectResult = null;
				antigravityDetectError = formatError(error);
			}
			return null;
		}
	}

	async function refreshAntigravityProfiles() {
		loadingAntigravityProfiles = true;
		try {
			const result = await track("antigravity-profiles", AntigravityService.ListProfiles());
			antigravityProfileSummaries = result.profiles ?? [];
			antigravityProfileError = "";
		} catch (error) {
			if (!isCancelError(error)) antigravityProfileError = formatError(error);
		} finally {
			loadingAntigravityProfiles = false;
		}
	}

	async function refreshClaudeCodeDetect() {
		try {
			const result = await track("claude-code-detect", ClaudeCodeService.Detect());
			claudeCodeDetectResult = result;
			claudeCodeDetectError = "";
			return result;
		} catch (error) {
			if (!isCancelError(error)) {
				claudeCodeDetectResult = null;
				claudeCodeDetectError = formatError(error);
			}
			return null;
		}
	}

	async function authorizeClaudeCodeKeychain() {
		try {
			const result = await track("claude-code-detect", ClaudeCodeService.AuthorizeKeychain());
			claudeCodeDetectResult = result;
			claudeCodeDetectError = "";
			return result;
		} catch (error) {
			if (!isCancelError(error)) {
				claudeCodeDetectResult = null;
				claudeCodeDetectError = formatError(error);
			}
			return null;
		}
	}

	async function refreshClaudeCodeProfiles() {
		loadingClaudeCodeProfiles = true;
		try {
			const result = await track("claude-code-profiles", ClaudeCodeService.ListProfiles());
			claudeCodeProfileSummaries = result.profiles ?? [];
			claudeCodeProfileError = "";
		} catch (error) {
			if (!isCancelError(error)) claudeCodeProfileError = formatError(error);
		} finally {
			loadingClaudeCodeProfiles = false;
		}
	}

	async function runDoctor() {
		await runAction("doctor", async () => {
			try {
				doctorResult = await track("doctor", DoctorService.Run());
				doctorError = "";
			} catch (error) {
				doctorError = formatError(error);
				throw error;
			}
		});
	}

	async function repairLock() {
		await runAction("repair-lock", async () => {
			await track("repair-lock", DoctorService.RepairLock(true));
			showNotice(translate("notice.lockOK.title"), translate("notice.lockOK.repaired"));
		});
	}

	async function retryRecoveryCleanup() {
		await runAction("retry-cleanup", async () => {
			await track("retry-cleanup", DoctorService.RetryRecoveryCleanup(true));
			doctorResult = await track("doctor", DoctorService.Run());
			doctorError = "";
			showNotice(
				translate("notice.recoveryCleanup.completedTitle"),
				translate("notice.recoveryCleanup.completedDescription"),
			);
		});
	}

	async function recoverOperation(operationID: string) {
		await runAction(`recover:${operationID}`, async () => {
			const result = await track("recover", DoctorService.RecoverOperation(operationID, true));
			if (!result.recovery_cleanup_completed) {
				toast.warning(translate("notice.recoveryCleanup.recoveryCompletedTitle"), {
					description: translate("notice.recoveryCleanup.recoveryCompletedDescription"),
				});
			} else {
				showNotice(
					translate(result.action === "close" ? "diagnosticsPage.recover.closedTitle" : "diagnosticsPage.recover.completedTitle"),
					translate(result.action === "close" ? "diagnosticsPage.recover.closedDescription" : "diagnosticsPage.recover.completedDescription"),
				);
			}
		});
	}

	async function runAction(name: string, action: () => Promise<void>) {
		if (actionBusy) return;
		actionBusy = name;
		try {
			await action();
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			if (actionBusy === name) actionBusy = "";
		}
	}

	function handleDashboardUpdate(payload: DashboardUpdatePayload | null | undefined) {
		if (!payload) return;
		if (payload.dashboard) applyDashboardResult(payload.dashboard);
		const grokBuildCurrentProfileID = dashboardActiveProfileID(payload.dashboard, grokBuildProviderID);
		const grokBuildEnabled = agentEnabled(payload.dashboard?.agents ?? [], "grok-build");
		const events = payload.events?.length ? payload.events : [payload.event];
		const changedProviders = new Set<string>();
		let reloadCodexRuntime = false;
		for (const event of events) {
			const switchedGrokBuildProfileID = event?.profile_id === grokBuildCurrentProfileID
				? grokBuildQuotaReadPolicy.afterSwitch(event, grokBuildEnabled)
				: "";
			if (switchedGrokBuildProfileID) {
				grokBuildQuota.setProfiles(grokBuildProfileSummaries, grokBuildCurrentProfileID);
				void grokBuildQuota.readCurrentQuota(switchedGrokBuildProfileID);
			}
			const switchedProfileID = antigravityQuotaReadPolicy.afterSwitch(event);
			if (switchedProfileID) {
				antigravityQuota.setProfiles(antigravityProfileSummaries);
				void antigravityQuota.readQuota(switchedProfileID);
			}
			if (event.error && !isCancelError(event.error)) showError(event.error);
			if (event.profile_changed || event.active_state_changed) changedProviders.add(event.provider_id ?? "");
			if (event.kind === "agent-state-changed" && event.agent_id === "codex" && event.agent_enabled) {
				reloadCodexRuntime = true;
			}
		}
		if (payload.error && !isCancelError(payload.error)) showError(payload.error);
		if (changedProviders.has(antigravityProviderID) && isAgentEnabled("antigravity")) void refreshAntigravityDetect();
		if (changedProviders.has(claudeCodeProviderID) && isAgentEnabled("claude-code")) void refreshClaudeCodeDetect();
		if (changedProviders.has(grokBuildProviderID) && isAgentEnabled("grok-build")) void refreshGrokBuildDetect();
		if (changedProviders.has(codexProviderID) && isAgentEnabled("codex")) void refreshDetect();
		if (reloadCodexRuntime) void codexRuntime.load();
	}

	function applyDashboardResult(next: DashboardResult) {
		dashboard = next;
		const grokBuildEnabled = agentEnabled(next.agents ?? [], "grok-build");
		const grokBuildCurrentProfileID = dashboardActiveProfileID(next, grokBuildProviderID);
		if (!agentEnabled(next.agents ?? [], "codex")) {
			detectResult = null;
			detectError = "";
			codexRuntime.reset();
		}
		if (!grokBuildEnabled) {
			grokBuildDetectResult = null;
			grokBuildDetectError = "";
			grokBuildQuota.reset("agent-disabled");
		}
		if (!agentEnabled(next.agents ?? [], "antigravity")) {
			antigravityDetectResult = null;
			antigravityDetectError = "";
			antigravityQuota.reset("agent-disabled");
		}
		if (!agentEnabled(next.agents ?? [], "claude-code")) {
			claudeCodeDetectResult = null;
			claudeCodeDetectError = "";
		}
		doctorResult = next.doctor ?? null;
		doctorError = "";
		codexProfileSummaries = next.codex_profiles?.profiles ?? [];
		loadingProfiles = false;
		codexConfigSets = next.codex_config_sets?.config_sets ?? [];
		grokBuildProfileSummaries = reconcileGrokBuildProfileSummaries(
			grokBuildProfileSummaries,
			grokBuildEnabled ? next.grok_build_profiles?.profiles : [],
			grokBuildCurrentProfileID,
		) as GrokBuildProfileSummary[];
		loadingGrokBuildProfiles = false;
		if (next.grok_build_config_sets) grokBuildConfigSets = next.grok_build_config_sets.config_sets ?? [];
		else if (!grokBuildEnabled) grokBuildConfigSets = [];
		antigravityProfileSummaries = next.antigravity_profiles?.profiles ?? [];
		loadingAntigravityProfiles = false;
		claudeCodeProfileSummaries = next.claude_code_profiles?.profiles ?? [];
		loadingClaudeCodeProfiles = false;
		if (next.startup_error) {
			dashboardError = desktopErrorMessage(next.startup_error, translate("errors.desktopUnavailable"));
			if (!startupRecoveryRedirected) {
				startupRecoveryRedirected = true;
				void push("/settings");
			}
		}
		else dashboardError = "";
	}

	function agentEnabled(states: DashboardResult["agents"], id: AgentID): boolean {
		return states?.some((state) => String(state.manifest.id) === id && state.enabled) ?? false;
	}

	function dashboardActiveProfileID(value: DashboardResult | null | undefined, providerID: string): string {
		return value?.active_states?.find((state) => state.provider_id === providerID)?.profile_id ?? "";
	}

	function isNavActive(view: WorkspaceView): boolean {
		return workspaceRoute.view === view;
	}

	function selectAgent(agentID: AgentID) {
		if (isAgentEnabled(agentID)) void push(agentHome(agentID));
	}

	function isAgentEnabled(agentID: AgentID): boolean {
		const states = dashboard?.agents;
		return !states?.length || states.some((state) => state.manifest.id === agentID && state.enabled);
	}

	function selectAgentTab(value: string) {
		if (selectedAgent !== "codex" && selectedAgent !== "grok-build") {
			if (selectedAgent) void push(agentHome(selectedAgent));
			return;
		}
		const basePath = selectedAgent === "grok-build" ? "/grok-build" : "/codex";
		switch (value) {
			case "profiles":
				void push(`${basePath}/profiles`);
				break;
			case "usage":
				void push(`${basePath}/usage`);
				break;
			case "settings":
				void push(`${basePath}/settings`);
				break;
		}
	}

	function selectGlobalSettingsTab(value: string) {
		if (value !== "general" && value !== "backups") return;
		globalSettingsTab = value;
		contentViewport?.scrollTo({ top: 0 });
	}

	function normalizeAppearance(value: string): Appearance {
		return value === "light" || value === "dark" ? value : "system";
	}

	function shortID(value: string): string {
		return value.length > 8 ? `…${value.slice(-8)}` : value;
	}

	function agentName(agentID: string): string {
		return dashboard?.agents?.find((state) => state.manifest.id === agentID)?.manifest.display_name
			|| agents.find((agent) => agent.id === agentID)?.name
			|| agentID;
	}

	function detectPlatform(): Platform {
		if (typeof navigator === "undefined") return "macos";
		const hint = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform || navigator.platform || navigator.userAgent;
		const normalized = hint.toLowerCase();
		if (normalized.includes("mac")) return "macos";
		if (normalized.includes("win")) return "windows";
		return "linux";
	}

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		inFlight.get(key)?.cancel("replaced");
		inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => {
			if (inFlight.get(key) === promise) inFlight.delete(key);
		}).catch(() => {});
		return promise;
	}

	function cancelDetect() {
		inFlight.get("detect")?.cancel("route-change");
		inFlight.delete("detect");
	}

	function cancelGrokBuildDetect() {
		inFlight.get("grok-build-detect")?.cancel("route-change");
		inFlight.delete("grok-build-detect");
	}

	function cancelAll() {
		for (const promise of inFlight.values()) promise.cancel("unmount");
		inFlight.clear();
	}

	function formatError(value: unknown): string {
		return desktopErrorMessage(value, translate("errors.desktopUnavailable"));
	}

	function showError(value: unknown) {
		const message = formatError(value);
		if (!message || lastToast === message) return;
		lastToast = message;
		toast.error(message);
	}

	function showNotice(title: string, description: string) {
		const key = `${title}:${description}`;
		if (lastToast === key) return;
		lastToast = key;
		toast.success(title, { description });
	}
</script>

<Sidebar.Provider
	bind:open={sidebarOpen}
	onOpenChange={(open) => { void saveSidebarState(open); }}
	class="block h-svh min-h-0 overflow-hidden"
	style="--sidebar-width: 10rem; --sidebar-width-icon: 3rem;"
>
	<header class="drag-region relative z-20 h-[52px] w-full shrink-0 border-b bg-background">
		<div
			class="flex h-full min-w-0 items-center justify-between gap-3 px-4 transition-[margin] duration-200 ease-linear"
			style={`margin-left: ${titlebarOffset};`}
		>
			<div class="flex min-w-0 items-center gap-2">
				<Sidebar.Trigger class="no-drag shrink-0" disabled={sidebarBusy} aria-label={$_("actions.toggleSidebar")} />
				<div class="h-4 shrink-0"><Separator orientation="vertical" /></div>
				<h1 class="truncate text-sm font-semibold tracking-tight">{pageTitle}</h1>
				{#if agentWorkspace}
					{#if currentProfileName}
						<Tooltip.Root>
							<Tooltip.Trigger>
								{#snippet child({ props })}
									<button
										{...props}
										type="button"
										class="no-drag inline-flex min-w-0 max-w-[min(20rem,40vw)] rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring"
										aria-label={$_("header.currentProfile", { values: { profile: currentProfileName } })}
									>
										<StatusBadge tone="current" class="max-w-full">
											<span class="truncate">{$_("header.currentProfile", { values: { profile: currentProfileName } })}</span>
										</StatusBadge>
									</button>
								{/snippet}
							</Tooltip.Trigger>
							<Tooltip.Content>{$_("header.currentProfile", { values: { profile: currentProfileName } })}</Tooltip.Content>
						</Tooltip.Root>
					{:else}
						<Badge variant="outline">{$_("header.noCurrentProfile")}</Badge>
					{/if}
				{/if}
			</div>
			<div class="no-drag flex shrink-0 items-center gap-2">
				{#if workspaceRoute.view === "diagnostics"}
					<Button size="sm" variant="outline" disabled={!!actionBusy} onclick={() => { void runDoctor(); }}>
						{#if actionBusy === "doctor"}<Spinner />{:else}<RefreshCwIcon data-icon="inline-start" />{/if}
						{$_("actions.recheck")}
					</Button>
				{:else if agentWorkspace}
					<DropdownMenu.Root>
						<DropdownMenu.Trigger>
							{#snippet child({ props })}
								<Button {...props} size="icon-sm" variant="ghost" aria-label={$_("actions.more")}><MoreHorizontalIcon /></Button>
							{/snippet}
						</DropdownMenu.Trigger>
						<DropdownMenu.Content align="end">
							<DropdownMenu.Group>
								<DropdownMenu.Item disabled={loading} onSelect={() => { void refreshAll(); }}>
									{#if loading}<Spinner />{:else}<RefreshCwIcon />{/if}
									{$_("actions.refresh")}
								</DropdownMenu.Item>
							</DropdownMenu.Group>
						</DropdownMenu.Content>
					</DropdownMenu.Root>
				{/if}
			</div>
		</div>
	</header>

	<div class="flex h-[calc(100svh-52px)] min-h-0 overflow-hidden">
		<Sidebar.Root collapsible="icon" class="md:top-[52px]! md:bottom-0! md:h-[calc(100svh-52px)]!">
			<Sidebar.Header class="no-drag shrink-0">
				<div class="flex h-8 min-w-0 items-center gap-2">
					<img src={lightThemeAppIconURL} alt="" draggable="false" class="size-8 shrink-0 select-none rounded-md dark:hidden" />
					<img src={darkThemeAppIconURL} alt="" draggable="false" class="hidden size-8 shrink-0 select-none rounded-md dark:block" />
					<div class="min-w-0 truncate text-sm font-semibold tracking-tight group-data-[collapsible=icon]:hidden">ProfileDeck</div>
				</div>
			</Sidebar.Header>

			<Sidebar.Content class="no-drag">
				<Sidebar.Group>
					<Sidebar.GroupLabel>{$_("nav.agents")}</Sidebar.GroupLabel>
					<Sidebar.GroupContent>
						<Sidebar.Menu>
							{#each enabledAgents as agent (agent.id)}
								{@const AgentIcon = agent.icon}
								<Sidebar.MenuItem>
									<Sidebar.MenuButton
										class="[&_svg]:size-5!"
										isActive={agent.id === selectedAgent}
										tooltipContent={agent.name}
										onclick={() => selectAgent(agent.id)}
									>
										<AgentIcon /><span class="group-data-[collapsible=icon]:hidden">{agent.name}</span>
									</Sidebar.MenuButton>
								</Sidebar.MenuItem>
							{/each}
						</Sidebar.Menu>
					</Sidebar.GroupContent>
				</Sidebar.Group>
			</Sidebar.Content>

			<Sidebar.Footer class="no-drag shrink-0">
				<Sidebar.Menu>
					{#if sidebarUpdate.visible}
						<Sidebar.MenuItem>
							{#if sidebarUpdate.ready}
								<Sidebar.MenuButton
									variant="outline"
									class="[&_svg]:size-5!"
									tooltipContent={sidebarUpdate.accessibleLabel}
									aria-label={sidebarUpdate.accessibleLabel}
									aria-disabled={!!updateBusy}
									onclick={restartWithUpdate}
								>
									{#if updateBusy === "restart"}<Spinner />{:else}<CircleArrowUpIcon />{/if}
									<span class="group-data-[collapsible=icon]:hidden">{sidebarUpdate.label}</span>
								</Sidebar.MenuButton>
							{:else}
								<Sidebar.MenuButton
									variant="outline"
									class="[&_svg]:size-5!"
									tooltipContent={sidebarUpdate.accessibleLabel}
								>
									{#snippet child({ props })}
										<div {...props} role="status" aria-label={sidebarUpdate.accessibleLabel}>
											<Spinner />
											<span class="group-data-[collapsible=icon]:hidden">{sidebarUpdate.label}</span>
										</div>
									{/snippet}
								</Sidebar.MenuButton>
							{/if}
						</Sidebar.MenuItem>
					{/if}
					<Sidebar.MenuItem>
						<Sidebar.MenuButton
							class="[&_svg]:size-5!"
							isActive={isNavActive("diagnostics")}
							tooltipContent={$_("nav.diagnostics")}
							onclick={() => push("/diagnostics")}
						>
							<StethoscopeIcon /><span class="group-data-[collapsible=icon]:hidden">{$_("nav.diagnostics")}</span>
						</Sidebar.MenuButton>
					</Sidebar.MenuItem>
					<Sidebar.MenuItem>
						<Sidebar.MenuButton
							class="[&_svg]:size-5!"
							isActive={isNavActive("settings")}
							tooltipContent={$_("nav.settings")}
							onclick={() => push("/settings")}
						>
							<SettingsIcon /><span class="group-data-[collapsible=icon]:hidden">{$_("nav.settings")}</span>
						</Sidebar.MenuButton>
					</Sidebar.MenuItem>
				</Sidebar.Menu>
			</Sidebar.Footer>
			<Sidebar.Rail class={sidebarBusy ? "pointer-events-none" : ""} />
		</Sidebar.Root>

		<Sidebar.Inset class="h-full min-h-0 min-w-0 overflow-hidden bg-background">
			{#if agentWorkspace}
				<div class="no-drag shrink-0 border-b px-4">
					<Tabs.Root value={activeAgentTab} onValueChange={selectAgentTab}>
						<Tabs.List variant="line" class="h-auto bg-transparent p-0">
							<Tabs.Trigger value="profiles">{$_("tabs.profiles")}</Tabs.Trigger>
							{#if selectedAgent === "codex" || selectedAgent === "grok-build"}
								<Tabs.Trigger value="usage">{$_("tabs.usage")}</Tabs.Trigger>
								<Tabs.Trigger value="settings">{$_("tabs.settings")}</Tabs.Trigger>
							{/if}
						</Tabs.List>
					</Tabs.Root>
				</div>
			{:else if workspaceRoute.view === "settings"}
				<div class="no-drag shrink-0 border-b px-4">
					<Tabs.Root value={globalSettingsTab} onValueChange={selectGlobalSettingsTab}>
						<Tabs.List variant="line" class="h-auto bg-transparent p-0">
							<Tabs.Trigger value="general">{$_("settings.tabs.general")}</Tabs.Trigger>
							<Tabs.Trigger value="backups">{$_("settings.tabs.backups")}</Tabs.Trigger>
						</Tabs.List>
					</Tabs.Root>
				</div>
			{/if}

			{#if dashboardError && workspaceRoute.view !== "diagnostics"}
				<div class="shrink-0 border-b px-4 py-3">
					<Alert.Root variant="destructive">
						<TriangleAlertIcon data-icon="inline-start" />
						<Alert.Title>{$_("diagnosticsPage.loadFailed")}</Alert.Title>
						<Alert.Description>{dashboardError}</Alert.Description>
						<Alert.Action>
							<div class="flex gap-2">
								<Button size="xs" variant="outline" onclick={() => { void refreshAll(); }}>{$_("actions.retry")}</Button>
								<Button size="xs" variant="outline" onclick={() => push("/diagnostics")}>{$_("nav.diagnostics")}</Button>
							</div>
						</Alert.Action>
					</Alert.Root>
				</div>
			{/if}

			{#if dashboard?.status.operation_recovery_cleanup_required && workspaceRoute.view !== "diagnostics"}
				<div class="shrink-0 border-b px-4 py-3">
					<Alert.Root variant="destructive">
						<TriangleAlertIcon data-icon="inline-start" />
						<Alert.Title>{$_("diagnosticsPage.finding.recoveryCleanupRequiredTitle")}</Alert.Title>
						<Alert.Description>{$_("diagnosticsPage.finding.recoveryCleanupRequiredDescription")}</Alert.Description>
						<Alert.Action>
							<Button size="xs" variant="outline" onclick={() => push("/diagnostics")}>{$_("nav.diagnostics")}</Button>
						</Alert.Action>
					</Alert.Root>
				</div>
			{/if}

			<div bind:this={contentViewport} class="min-h-0 flex-1 overflow-auto p-4">
				{#if !agentWorkspaceReady}
					{#if loading}<div class="grid h-full place-items-center"><Spinner /></div>{/if}
				{:else if workspaceRoute.view === "profiles"}
					{#await loadCodexProfiles()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: CodexProfiles }}
						<CodexProfiles
							route={workspaceRoute.codexProfile}
							profiles={codexProfileSummaries}
							dashboardConfigSets={codexConfigSets}
							{detectResult}
							{detectError}
							activeProfileID={codexActiveProfileID}
							{loadingProfiles}
							{profileError}
							{useRequest}
							{refreshDetect}
							refreshProfiles={refreshCodexProfiles}
							{cancelDetect}
							onUseRequestHandled={(sequence) => { if (useRequest?.sequence === sequence) useRequest = null; }}
							{showError}
							{showNotice}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else if workspaceRoute.view === "grok-build-profiles"}
					{#await loadGrokBuildProfiles()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: GrokBuildProfiles }}
						<GrokBuildProfiles
							route={workspaceRoute.grokBuildProfile}
							profiles={grokBuildProfileSummaries}
							dashboardConfigSets={grokBuildConfigSets}
							detectResult={grokBuildDetectResult}
							detectError={grokBuildDetectError}
							activeProfileID={grokBuildActiveProfileID}
							loadingProfiles={loadingGrokBuildProfiles}
							profileError={grokBuildProfileError}
							useRequest={grokBuildUseRequest}
							refreshDetect={refreshGrokBuildDetect}
							refreshProfiles={refreshGrokBuildProfiles}
							quotaForSummary={(summary) => grokBuildQuota.quotaForSummary(summary)}
							quotaCheckForSummary={(summary) => grokBuildQuota.checkForSummary(summary)}
							quotaLoading={(profileID) => grokBuildQuota.isLoading(profileID)}
							refreshQuota={(profileID) => grokBuildQuota.readQuota(profileID)}
							cancelDetect={cancelGrokBuildDetect}
							onUseRequestHandled={(sequence) => {
								if (grokBuildUseRequest?.sequence === sequence) grokBuildUseRequest = null;
							}}
							{showError}
							{showNotice}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else if workspaceRoute.view === "antigravity-profiles"}
					{#await loadAntigravityProfiles()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: AntigravityProfiles }}
						<AntigravityProfiles
							route={workspaceRoute.antigravityProfile}
							profiles={antigravityProfileSummaries}
							detectResult={antigravityDetectResult}
							detectError={antigravityDetectError}
							activeProfileID={antigravityActiveProfileID}
							loadingProfiles={loadingAntigravityProfiles}
							profileError={antigravityProfileError}
							useRequest={antigravityUseRequest}
							refreshDetect={refreshAntigravityDetect}
							refreshProfiles={refreshAntigravityProfiles}
							quotaForSummary={(summary) => antigravityQuota.quotaForSummary(summary)}
							quotaCheckForSummary={(summary) => antigravityQuota.checkForSummary(summary)}
							quotaLoading={(profileID) => antigravityQuota.isLoading(profileID)}
							refreshQuota={(profileID) => antigravityQuota.readQuota(profileID)}
							onUseRequestHandled={(sequence) => { if (antigravityUseRequest?.sequence === sequence) antigravityUseRequest = null; }}
							{showError}
							{showNotice}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else if workspaceRoute.view === "claude-code-profiles"}
					{#await loadClaudeCodeProfiles()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: ClaudeCodeProfiles }}
						<ClaudeCodeProfiles
							route={workspaceRoute.claudeCodeProfile}
							profiles={claudeCodeProfileSummaries}
							detectResult={claudeCodeDetectResult}
							detectError={claudeCodeDetectError}
							activeProfileID={claudeCodeActiveProfileID}
							loadingProfiles={loadingClaudeCodeProfiles}
							profileError={claudeCodeProfileError}
							useRequest={claudeCodeUseRequest}
							refreshDetect={refreshClaudeCodeDetect}
							authorizeKeychain={authorizeClaudeCodeKeychain}
							refreshProfiles={refreshClaudeCodeProfiles}
							onUseRequestHandled={(sequence) => { if (claudeCodeUseRequest?.sequence === sequence) claudeCodeUseRequest = null; }}
							{showError}
							{showNotice}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else if workspaceRoute.view === "usage" || workspaceRoute.view === "grok-build-usage"}
					{#key `${usageProviderID}:${usageProviderExists}`}
						{#await loadUsagePage()}
							<WorkspaceViewStatus state="loading" />
						{:then { default: UsagePage }}
							<UsagePage
								providerID={usageProviderID}
								providerName={usageProviderName}
								providerExists={usageProviderExists}
								onOpenProfiles={() => push(usageProviderID === grokBuildProviderID ? "/grok-build/profiles" : "/codex/profiles")}
								{showError}
							/>
						{:catch}
							<WorkspaceViewStatus state="error" />
						{/await}
					{/key}
				{:else if workspaceRoute.view === "codex-settings"}
					{#await loadCodexSettings()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: CodexSettings }}
						<CodexSettings />
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else if workspaceRoute.view === "grok-build-settings"}
					{#await loadGrokBuildSettings()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: GrokBuildSettings }}
						<GrokBuildSettings
							providerExists={grokBuildProviderExists}
							onOpenProfiles={() => push("/grok-build/profiles")}
							{showError}
							{showNotice}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else if workspaceRoute.view === "settings"}
					{#await loadGlobalSettings()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: GlobalSettings }}
						<GlobalSettings
							section={globalSettingsTab}
							language={languagePreference}
							{appearance}
							{languageBusy}
							{appearanceBusy}
							agents={dashboard?.agents ?? []}
							{agentBusy}
							{updateStatus}
							{updateBusy}
							onLanguageChange={changeLanguage}
							onAppearanceChange={changeAppearance}
							onAgentEnabledChange={changeAgentEnabled}
							onChannelChange={changeUpdateChannel}
							onAutomaticChange={changeAutomaticUpdates}
							onCheckForUpdates={checkForUpdates}
							onRestart={restartWithUpdate}
							{automaticBackups}
							databaseHealthy={dashboard?.status.schema_healthy ?? false}
							onAutomaticBackupsChange={(enabled) => { automaticBackups = enabled; }}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{:else}
					{#await loadDiagnosticsPage()}
						<WorkspaceViewStatus state="loading" />
					{:then { default: DiagnosticsPage }}
						<DiagnosticsPage
							doctor={doctorResult}
							loading={actionBusy === "doctor"}
							error={doctorError || dashboardError}
							{actionBusy}
							onRecheck={runDoctor}
							onRepair={repairLock}
							onRetryCleanup={retryRecoveryCleanup}
							onRecover={recoverOperation}
						/>
					{:catch}
						<WorkspaceViewStatus state="error" />
					{/await}
				{/if}
			</div>
		</Sidebar.Inset>
	</div>
</Sidebar.Provider>
