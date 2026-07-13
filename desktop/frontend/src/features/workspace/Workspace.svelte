<script lang="ts">
	import { onMount, untrack } from "svelte";
	import { Events, type CancellablePromise } from "@wailsio/runtime";
	import { setMode } from "mode-watcher";
	import { push, replace, router } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import BotIcon from "@lucide/svelte/icons/bot";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import OrbitIcon from "@lucide/svelte/icons/orbit";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import SettingsIcon from "@lucide/svelte/icons/settings";
	import StethoscopeIcon from "@lucide/svelte/icons/stethoscope";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import {
		AppService,
		AntigravityService,
		BackupService,
		ClaudeCodeService,
		CodexService,
		DoctorService,
		SettingsService,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { DashboardResult, DesktopError } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		AntigravityDetectResult,
		AntigravityProfileSummary,
		CodexConfigSet,
		CodexDetectResult,
		CodexProfileSummary,
		ClaudeCodeDetectResult,
		ClaudeCodeProfileSummary,
		DoctorResult,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
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
		normalizeDesktopLanguage,
		translate,
		type DesktopLanguage,
	} from "$lib/i18n";
	import AntigravityProfiles from "../profiles/AntigravityProfiles.svelte";
	import CodexProfiles from "../profiles/CodexProfiles.svelte";
	import ClaudeCodeProfiles from "../profiles/ClaudeCodeProfiles.svelte";
	import type { AntigravityProfileRoute, ClaudeCodeProfileRoute, CodexProfileRoute, ProfileUseRequest } from "../profiles/types";
	import CodexSettings from "../settings/CodexSettings.svelte";
	import { provideCodexRuntime } from "../settings/codex-runtime.svelte.js";
	import UsagePage from "../usage/UsagePage.svelte";
	import DiagnosticsPage from "./DiagnosticsPage.svelte";
	import GlobalSettings from "./GlobalSettings.svelte";

	type WorkspaceView = "profiles" | "antigravity-profiles" | "claude-code-profiles" | "usage" | "codex-settings" | "settings" | "diagnostics";
	type AgentID = "codex" | "antigravity" | "claude-code";
	type Appearance = "system" | "light" | "dark";
	type Platform = "macos" | "windows" | "linux";

	type DesktopChangeEvent = {
		kind: string;
		status?: string;
		error?: DesktopError | null;
		profile_changed?: boolean;
		config_sets_changed?: boolean;
		active_state_changed?: boolean;
		provider_id?: string;
	};

	type DashboardUpdatePayload = {
		event: DesktopChangeEvent;
		dashboard: DashboardResult;
		error?: DesktopError | null;
	};

	type WorkspaceRoute = {
		view: WorkspaceView;
		codexProfile: CodexProfileRoute;
		antigravityProfile: AntigravityProfileRoute;
		claudeCodeProfile: ClaudeCodeProfileRoute;
		valid: boolean;
	};

	const codexProviderID = "codex";
	const antigravityProviderID = "antigravity";
	const claudeCodeProviderID = "claude-code";
	const agents: Array<{ id: AgentID; name: string; icon: typeof BotIcon }> = [
		{ id: "codex", name: "Codex", icon: BotIcon },
		{ id: "antigravity", name: "Antigravity", icon: OrbitIcon },
		{ id: "claude-code", name: "Claude Code", icon: BotIcon },
	];
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let platform = $state<Platform>(detectPlatform());
	let loading = $state(false);
	let loadingProfiles = $state(true);
	let loadingAntigravityProfiles = $state(true);
	let loadingClaudeCodeProfiles = $state(true);
	let actionBusy = $state("");
	let languageBusy = $state(false);
	let appearanceBusy = $state(false);
	let sidebarBusy = $state(false);
	let languagePreference = $state<DesktopLanguage>("auto");
	let appearance = $state<Appearance>("system");
	let persistedAppearance = $state<Appearance>("system");
	let sidebarOpen = $state(true);
	let persistedSidebarCollapsed = $state(false);
	let settingsLoaded = $state(false);
	let lastToast = "";
	let invalidRoute = "";
	let currentPath = $state(router.location);

	let dashboard = $state<DashboardResult | null>(null);
	let detectResult = $state<CodexDetectResult | null>(null);
	let antigravityDetectResult = $state<AntigravityDetectResult | null>(null);
	let claudeCodeDetectResult = $state<ClaudeCodeDetectResult | null>(null);
	let doctorResult = $state<DoctorResult | null>(null);
	let codexProfileSummaries = $state<CodexProfileSummary[]>([]);
	let antigravityProfileSummaries = $state<AntigravityProfileSummary[]>([]);
	let claudeCodeProfileSummaries = $state<ClaudeCodeProfileSummary[]>([]);
	let codexConfigSets = $state<CodexConfigSet[]>([]);
	let dashboardError = $state("");
	let detectError = $state("");
	let antigravityDetectError = $state("");
	let claudeCodeDetectError = $state("");
	let doctorError = $state("");
	let profileError = $state("");
	let antigravityProfileError = $state("");
	let claudeCodeProfileError = $state("");
	let useRequest = $state<ProfileUseRequest | null>(null);
	let antigravityUseRequest = $state<ProfileUseRequest | null>(null);
	let claudeCodeUseRequest = $state<ProfileUseRequest | null>(null);
	let useRequestSequence = 0;
	let startupQuotaReadStarted = false;

	const codexRuntime = provideCodexRuntime({ showError, showNotice });
	let workspaceRoute = $derived(parseWorkspaceRoute(currentPath));
	let agentWorkspace = $derived(isAgentWorkspace(workspaceRoute.view));
	let selectedAgent = $derived<AgentID | null>(workspaceRoute.view === "antigravity-profiles" ? "antigravity" : workspaceRoute.view === "claude-code-profiles" ? "claude-code" : agentWorkspace ? "codex" : null);
	let activeAgentTab = $derived(workspaceRoute.view === "codex-settings" ? "settings" : workspaceRoute.view === "antigravity-profiles" || workspaceRoute.view === "claude-code-profiles" ? "profiles" : workspaceRoute.view);
	let codexActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === codexProviderID)?.profile_id ?? "");
	let antigravityActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === antigravityProviderID)?.profile_id ?? "");
	let claudeCodeActiveProfileID = $derived(dashboard?.active_states?.find((state) => state.provider_id === claudeCodeProviderID)?.profile_id ?? "");
	let activeProfileID = $derived(selectedAgent === "antigravity" ? antigravityActiveProfileID : selectedAgent === "claude-code" ? claudeCodeActiveProfileID : codexActiveProfileID);
	let currentProfileName = $derived.by(() => {
		void $locale;
		const active = selectedAgent === "antigravity"
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
			default: return selectedAgent === "antigravity" ? "Antigravity" : selectedAgent === "claude-code" ? "Claude Code" : "Codex";
		}
	});
	let titlebarOffset = $derived(sidebarOpen ? "10rem" : platform === "macos" ? "5rem" : "3rem");

	$effect(() => {
		const profiles = codexProfileSummaries;
		untrack(() => codexRuntime.setProfiles(profiles));
	});

	$effect(() => {
		const profileID = codexActiveProfileID;
		if (!profileID || startupQuotaReadStarted) return;
		startupQuotaReadStarted = true;
		untrack(() => { void codexRuntime.readQuota(profileID); });
	});

	$effect(() => {
		const path = currentPath;
		if (path === "/codex/health") {
			void replace("/diagnostics");
			return;
		}
		if (workspaceRoute.valid) {
			invalidRoute = "";
		} else if (invalidRoute !== path) {
			invalidRoute = path;
			void replace(path.startsWith("/antigravity/") ? "/antigravity/profiles" : path.startsWith("/claude-code/") ? "/claude-code/profiles" : "/codex/profiles");
		}
	});

	onMount(() => {
		const syncPath = () => {
			currentPath = router.location;
		};
		window.addEventListener("hashchange", syncPath);
		syncPath();
		void loadSettings();
		const stopRuntime = codexRuntime.start();
		void refreshAll(false);
		const off = [
			Events.On("profiledeck:open-switch", (event) => {
				const payload = event.data ?? {};
				if (!payload.profile_id) return;
				if (payload.provider_id === codexProviderID) {
					useRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
					void push("/codex/profiles");
				} else if (payload.provider_id === antigravityProviderID) {
					antigravityUseRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
					void push("/antigravity/profiles");
				} else if (payload.provider_id === claudeCodeProviderID) {
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
		];

		return () => {
			window.removeEventListener("hashchange", syncPath);
			for (const dispose of off) dispose();
			stopRuntime();
			cancelAll();
		};
	});

	async function refreshAll(reloadRuntime = true) {
		loading = true;
		try {
			const detectCurrentAgent = selectedAgent === "antigravity" ? refreshAntigravityDetect() : selectedAgent === "claude-code" ? refreshClaudeCodeDetect() : refreshDetect();
			const [dashboardResult] = await Promise.all([
				track("dashboard", AppService.Dashboard()),
				detectCurrentAgent,
			]);
			applyDashboardResult(dashboardResult);
			dashboardError = "";
			if (reloadRuntime) await codexRuntime.load();
		} catch (error) {
			if (!isCancelError(error)) {
				dashboardError = formatError(error);
				showError(error);
			}
			loadingProfiles = false;
			loadingAntigravityProfiles = false;
			loadingClaudeCodeProfiles = false;
		} finally {
			loading = false;
		}
	}

	async function loadSettings() {
		try {
			const settings = await track("settings", SettingsService.Get());
			languagePreference = applyDesktopLanguagePreference(settings.language);
			appearance = normalizeAppearance(settings.appearance);
			persistedAppearance = appearance;
			setMode(appearance);
			persistedSidebarCollapsed = settings.sidebar_collapsed;
			sidebarOpen = !settings.sidebar_collapsed;
			settingsLoaded = true;
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		}
	}

	async function changeLanguage(value: string) {
		const next = normalizeDesktopLanguage(value);
		if (next === languagePreference || languageBusy) return;
		const previous = languagePreference;
		languagePreference = applyDesktopLanguagePreference(next);
		languageBusy = true;
		try {
			const settings = await track("settings-language", SettingsService.Update({ config_dir: "", language: next }));
			languagePreference = applyDesktopLanguagePreference(settings.language);
			showNotice(translate("notice.settingsSaved.title"), translate("notice.settingsSaved.description"));
		} catch (error) {
			languagePreference = applyDesktopLanguagePreference(previous);
			if (!isCancelError(error)) showError(error);
		} finally {
			languageBusy = false;
		}
	}

	async function changeAppearance(value: string) {
		const next = normalizeAppearance(value);
		if (next === appearance || appearanceBusy) return;
		const previous = persistedAppearance;
		appearance = next;
		setMode(next);
		appearanceBusy = true;
		try {
			const settings = await track("settings-appearance", SettingsService.Update({ config_dir: "", appearance: next }));
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

	async function saveSidebarState(open: boolean) {
		if (!settingsLoaded) return;
		if (sidebarBusy) {
			sidebarOpen = !persistedSidebarCollapsed;
			return;
		}
		sidebarBusy = true;
		try {
			const settings = await SettingsService.Update({ config_dir: "", sidebar_collapsed: !open });
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

	async function recoverOperation(operationID: string) {
		await runAction(`recover:${operationID}`, async () => {
			await track("recover", BackupService.RecoverFailedSwitch(operationID, true));
			showNotice(translate("diagnosticsPage.recover.completedTitle"), translate("diagnosticsPage.recover.completedDescription"));
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
		if (payload.error && !isCancelError(payload.error)) showError(payload.error);
		if (payload.event?.error && !isCancelError(payload.event.error)) showError(payload.event.error);
		if (payload.event?.profile_changed || payload.event?.active_state_changed) {
			if (payload.event.provider_id === antigravityProviderID) void refreshAntigravityDetect();
			else if (payload.event.provider_id === claudeCodeProviderID) void refreshClaudeCodeDetect();
			else void refreshDetect();
		}
	}

	function applyDashboardResult(next: DashboardResult) {
		dashboard = next;
		doctorResult = next.doctor ?? null;
		doctorError = "";
		codexProfileSummaries = next.codex_profiles?.profiles ?? [];
		loadingProfiles = false;
		codexConfigSets = next.codex_config_sets?.config_sets ?? [];
		antigravityProfileSummaries = next.antigravity_profiles?.profiles ?? [];
		loadingAntigravityProfiles = false;
		claudeCodeProfileSummaries = next.claude_code_profiles?.profiles ?? [];
		loadingClaudeCodeProfiles = false;
		if (next.startup_error) dashboardError = desktopErrorMessage(next.startup_error, translate("errors.desktopUnavailable"));
		else dashboardError = "";
	}

	function parseWorkspaceRoute(path: string): WorkspaceRoute {
		const codexList: CodexProfileRoute = { kind: "list", profileID: "" };
		const antigravityList: AntigravityProfileRoute = { kind: "list", profileID: "" };
		const claudeCodeList: ClaudeCodeProfileRoute = { kind: "list", profileID: "" };
		const build = (view: WorkspaceView, codexProfile: CodexProfileRoute = codexList, antigravityProfile: AntigravityProfileRoute = antigravityList, claudeCodeProfile: ClaudeCodeProfileRoute = claudeCodeList): WorkspaceRoute => ({ view, codexProfile, antigravityProfile, claudeCodeProfile, valid: true });
		const list = (): WorkspaceRoute => build("profiles");
		if (path === "/" || path === "/codex/profiles") return list();
		if (path === "/codex/config-sets") return build("profiles", { kind: "config-sets", profileID: "" });
		if (path === "/codex/profiles/new") return build("profiles", { kind: "new", profileID: "" });
		const fork = path.match(/^\/codex\/profiles\/([^/]+)\/fork$/);
		if (fork) {
			const profileID = decodeRouteID(fork[1]);
			return profileID ? build("profiles", { kind: "fork", profileID }) : { ...list(), valid: false };
		}
		const detail = path.match(/^\/codex\/profiles\/([^/]+)$/);
		if (detail) {
			const profileID = decodeRouteID(detail[1]);
			return profileID ? build("profiles", { kind: "detail", profileID }) : { ...list(), valid: false };
		}
		if (path === "/antigravity/profiles") return build("antigravity-profiles");
		if (path === "/antigravity/profiles/new") return build("antigravity-profiles", codexList, { kind: "new", profileID: "" });
		const antigravityDetail = path.match(/^\/antigravity\/profiles\/([^/]+)$/);
		if (antigravityDetail) {
			const profileID = decodeRouteID(antigravityDetail[1]);
			return profileID ? build("antigravity-profiles", codexList, { kind: "detail", profileID }) : { ...build("antigravity-profiles"), valid: false };
		}
		if (path === "/claude-code/profiles") return build("claude-code-profiles");
		if (path === "/claude-code/profiles/new") return build("claude-code-profiles", codexList, antigravityList, { kind: "new", profileID: "" });
		const claudeCodeDetail = path.match(/^\/claude-code\/profiles\/([^/]+)$/);
		if (claudeCodeDetail) {
			const profileID = decodeRouteID(claudeCodeDetail[1]);
			return profileID ? build("claude-code-profiles", codexList, antigravityList, { kind: "detail", profileID }) : { ...build("claude-code-profiles"), valid: false };
		}
		if (path === "/codex/usage") return build("usage");
		if (path === "/codex/settings") return build("codex-settings");
		if (path === "/settings") return build("settings");
		if (path === "/diagnostics" || path === "/codex/health") return build("diagnostics");
		return { ...list(), valid: false };
	}

	function decodeRouteID(value: string): string | null {
		try {
			const decoded = decodeURIComponent(value);
			return decoded.length <= 80 && /^[a-z0-9][a-z0-9._-]*$/.test(decoded) ? decoded : null;
		} catch {
			return null;
		}
	}

	function isNavActive(view: WorkspaceView): boolean {
		return workspaceRoute.view === view;
	}

	function isAgentWorkspace(view: WorkspaceView): boolean {
		return view === "profiles" || view === "antigravity-profiles" || view === "claude-code-profiles" || view === "usage" || view === "codex-settings";
	}

	function selectAgent(agentID: AgentID) {
		if (agentID === "codex") void push("/codex/profiles");
		else if (agentID === "antigravity") void push("/antigravity/profiles");
		else void push("/claude-code/profiles");
	}

	function selectAgentTab(value: string) {
		if (selectedAgent !== "codex") {
			void push(selectedAgent === "claude-code" ? "/claude-code/profiles" : "/antigravity/profiles");
			return;
		}
		switch (value) {
			case "profiles":
				void push("/codex/profiles");
				break;
			case "usage":
				void push("/codex/usage");
				break;
			case "settings":
				void push("/codex/settings");
				break;
		}
	}

	function normalizeAppearance(value: string): Appearance {
		return value === "light" || value === "dark" ? value : "system";
	}

	function shortID(value: string): string {
		return value.length > 8 ? `…${value.slice(-8)}` : value;
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
					<div class="grid size-8 shrink-0 select-none place-items-center rounded-md bg-primary text-sm font-bold text-primary-foreground">P</div>
					<div class="min-w-0 truncate text-sm font-semibold tracking-tight group-data-[collapsible=icon]:hidden">ProfileDeck</div>
				</div>
			</Sidebar.Header>

			<Sidebar.Content class="no-drag">
				<Sidebar.Group>
					<Sidebar.GroupLabel>{$_("nav.agents")}</Sidebar.GroupLabel>
					<Sidebar.GroupContent>
						<Sidebar.Menu>
							{#each agents as agent (agent.id)}
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
							{#if selectedAgent === "codex"}
								<Tabs.Trigger value="usage">{$_("tabs.usage")}</Tabs.Trigger>
								<Tabs.Trigger value="settings">{$_("tabs.settings")}</Tabs.Trigger>
							{/if}
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

			<div class="min-h-0 flex-1 overflow-auto p-4">
				{#if workspaceRoute.view === "profiles"}
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
				{:else if workspaceRoute.view === "antigravity-profiles"}
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
						onUseRequestHandled={(sequence) => { if (antigravityUseRequest?.sequence === sequence) antigravityUseRequest = null; }}
						{showError}
						{showNotice}
					/>
				{:else if workspaceRoute.view === "claude-code-profiles"}
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
				{:else if workspaceRoute.view === "usage"}
					<UsagePage {showError} />
				{:else if workspaceRoute.view === "codex-settings"}
					<CodexSettings />
				{:else if workspaceRoute.view === "settings"}
					<GlobalSettings
						language={languagePreference}
						{appearance}
						{languageBusy}
						{appearanceBusy}
						onLanguageChange={changeLanguage}
						onAppearanceChange={changeAppearance}
					/>
				{:else}
					<DiagnosticsPage
						doctor={doctorResult}
						loading={actionBusy === "doctor"}
						error={doctorError || dashboardError}
						{actionBusy}
						onRecheck={runDoctor}
						onRepair={repairLock}
						onRecover={recoverOperation}
					/>
				{/if}
			</div>
		</Sidebar.Inset>
	</div>
</Sidebar.Provider>
