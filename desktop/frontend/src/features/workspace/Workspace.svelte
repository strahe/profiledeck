<script lang="ts">
	import { onMount } from "svelte";
	import { Events, type CancellablePromise } from "@wailsio/runtime";
	import { setMode } from "mode-watcher";
	import { push, replace, router } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";

	import {
		AppService,
		CodexService,
		DoctorService,
		SettingsService,
		UsageService,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { DashboardResult, DesktopError } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexDetectResult,
		CodexProfileSummary,
		DoctorResult,
		UsageSummaryResult,
		UsageSyncResult,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	import * as Empty from "$lib/components/ui/empty";
	import * as Field from "$lib/components/ui/field";
	import * as Select from "$lib/components/ui/select";
	import * as Table from "$lib/components/ui/table";
	import * as Tabs from "$lib/components/ui/tabs";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Spinner } from "$lib/components/ui/spinner";
	import { desktopErrorMessage, isCancelError } from "$lib/desktop-errors";
	import {
		applyDesktopLanguagePreference,
		currentDesktopLocale,
		normalizeDesktopLanguage,
		translate,
		type DesktopLanguage,
	} from "$lib/i18n";
	import { cn } from "$lib/utils";
	import CodexProfiles from "../profiles/CodexProfiles.svelte";
	import type { CodexProfileRoute, ProfileUseRequest } from "../profiles/types";

	type AgentID = "codex" | "claude" | "gemini" | "opencode";
	type PlaceholderAgentID = Exclude<AgentID, "codex">;
	type WorkspaceTab = "profiles" | "health" | "usage";
	type WorkspaceView = WorkspaceTab | "settings";
	type StatusVariant = "ok" | "warn" | "muted";
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

	type WorkspaceRoute = {
		view: WorkspaceView;
		profile: CodexProfileRoute;
		valid: boolean;
	};

	type AgentProfile = {
		id: string;
		name: string;
		description: string;
		model: string;
		provider: string;
		account: string;
	};

	type AgentData = {
		id: AgentID;
		name: string;
		detected: boolean;
		current: string;
		profiles: AgentProfile[];
		usage: { events: string; input: string; output: string; cost: string };
		health: {
			overall: string;
			lock: string;
			pending: number;
			failed: number;
			findings: Array<{ id: string; status: string; message: string }>;
		};
		placeholder: boolean;
	};

	const codexProviderID = "codex";
	const tabs: Array<{ id: WorkspaceTab; labelKey: string }> = [
		{ id: "profiles", labelKey: "tabs.profiles" },
		{ id: "health", labelKey: "tabs.health" },
		{ id: "usage", labelKey: "tabs.usage" },
	];
	const agentOrder: AgentID[] = ["codex", "claude", "gemini", "opencode"];
	const desktopChangeKind = { codexProfileChanged: "codex-profile-changed" } as const;
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	const initialPlaceholders: Record<PlaceholderAgentID, AgentData> = {
		claude: {
			id: "claude",
			name: "Claude",
			detected: true,
			current: "personal",
			profiles: [
				{ id: "personal", name: "personal", description: "Personal account", model: "claude-sonnet-4-6", provider: "anthropic", account: "" },
				{ id: "client-a", name: "client-a", description: "Client A workspace", model: "claude-sonnet-4-6", provider: "anthropic", account: "" },
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
			profiles: [],
			usage: { events: "—", input: "—", output: "—", cost: "—" },
			health: { overall: "—", lock: "—", pending: 0, failed: 0, findings: [] },
			placeholder: true,
		},
	};

	let selectedAgent = $state<AgentID>("codex");
	let selectedView = $state<WorkspaceView>("profiles");
	let selectedTab = $state<WorkspaceTab>("profiles");
	let theme = $state<"light" | "dark">("light");
	let platform = $state<Platform>(detectPlatform());
	let loading = $state(false);
	let loadingProfiles = $state(false);
	let actionBusy = $state("");
	let languageBusy = $state(false);
	let languagePreference = $state<DesktopLanguage>("auto");
	let lastToast = "";
	let invalidRoute = "";

	let dashboard = $state<DashboardResult | null>(null);
	let detectResult = $state<CodexDetectResult | null>(null);
	let doctorResult = $state<DoctorResult | null>(null);
	let usageSummary = $state<UsageSummaryResult | null>(null);
	let usageSyncResult = $state<UsageSyncResult | null>(null);
	let codexProfileSummaries = $state<CodexProfileSummary[]>([]);
	let dashboardError = $state("");
	let detectError = $state("");
	let profileError = $state("");
	let usageError = $state("");
	let lastRefreshSummary = $state("");
	let placeholderAgents = $state<Record<PlaceholderAgentID, AgentData>>(initialPlaceholders);
	let useRequest = $state<ProfileUseRequest | null>(null);
	let useRequestSequence = 0;

	let workspaceRoute = $derived(parseWorkspaceRoute(router.location));
	let codexAgent = $derived.by(() => {
		void $locale;
		return buildCodexAgent();
	});
	let currentAgent = $derived(agentByID(selectedAgent));

	$effect(() => {
		const route = workspaceRoute;
		selectedView = route.view;
		if (route.view !== "settings") selectedTab = route.view;
		if (route.profile.kind !== "list" && route.view === "profiles") selectedAgent = "codex";
		if (route.valid) {
			invalidRoute = "";
		} else if (invalidRoute !== router.location) {
			selectedAgent = "codex";
			invalidRoute = router.location;
			void replace("/codex/profiles");
		}
	});

	onMount(() => {
		applyTheme(theme);
		lastRefreshSummary = translate("diagnostics.waiting");
		applyDesktopLanguagePreference(languagePreference);
		void loadSettings();
		void refreshAll();

		const off = [
			Events.On("profiledeck:open-switch", (event) => {
				const payload = event.data ?? {};
				if (payload.provider_id !== codexProviderID || !payload.profile_id) return;
				selectedAgent = "codex";
				useRequest = { profileID: payload.profile_id, sequence: ++useRequestSequence };
				void push("/codex/profiles");
			}),
			Events.On("profiledeck:open-doctor", () => {
				selectedAgent = "codex";
				void push("/codex/health");
				void runHealth();
			}),
			Events.On("profiledeck:usage-synced", (event) => {
				usageSyncResult = event.data ?? null;
				showNotice(translate("notice.usageSynced.title"), translate("notice.usageSynced.codexDescription"));
			}),
			Events.On("profiledeck:dashboard-updated", (event) => handleDashboardUpdate(event.data as DashboardUpdatePayload)),
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
		lastRefreshSummary = translate("diagnostics.loading");
		try {
			try {
				const next = await track("dashboard", AppService.Dashboard());
				applyDashboardResult(next);
				dashboardError = "";
			} catch (error) {
				if (!isCancelError(error)) {
					dashboardError = formatError(error);
					showError(error);
				}
			}
			await Promise.allSettled([refreshDetect(), refreshCodexProfiles(), refreshUsage()]);
		} finally {
			updateRefreshSummary();
			loading = false;
		}
	}

	async function loadSettings() {
		try {
			const settings = await track("settings", SettingsService.Get());
			languagePreference = applyDesktopLanguagePreference(settings.language);
			updateRefreshSummary();
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		}
	}

	async function changeLanguage(value: string) {
		const next = normalizeDesktopLanguage(value);
		if (next === languagePreference && !languageBusy) return;
		languagePreference = applyDesktopLanguagePreference(next);
		updateRefreshSummary();
		languageBusy = true;
		try {
			const settings = await track("settings-update", SettingsService.Update({ config_dir: "", language: next }));
			languagePreference = applyDesktopLanguagePreference(settings.language);
			updateRefreshSummary();
			showNotice(translate("notice.settingsSaved.title"), translate("notice.settingsSaved.description"));
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			languageBusy = false;
		}
	}

	async function navigateToView(view: WorkspaceView) {
		if (view === "settings") await push("/settings");
		else await push(`/codex/${view}`);
	}

	async function selectAgent(agentID: AgentID) {
		selectedAgent = agentID;
		await push("/codex/profiles");
	}

	async function refreshDetect() {
		try {
			detectResult = await track("detect", CodexService.Detect());
			detectError = "";
		} catch (error) {
			if (!isCancelError(error)) {
				detectResult = null;
				detectError = formatError(error);
			}
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

	async function refreshUsage() {
		try {
			usageSummary = await track("usage-summary", UsageService.Summary(codexProviderID));
			usageError = "";
		} catch (error) {
			if (!isCancelError(error)) {
				usageSummary = null;
				usageError = formatError(error);
			}
		}
	}

	async function detectSelectedAgent() {
		if (selectedAgent !== "codex") {
			showNotice(translate("notice.detected.title"), translate("notice.detected.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await runAction("detect", async () => {
			await refreshDetect();
			showNotice(translate("notice.detected.title"), translate("notice.detected.codexDescription"));
		});
	}

	async function refreshSelectedAgent() {
		if (selectedAgent !== "codex") {
			showNotice(translate("notice.refreshed.title"), translate("notice.refreshed.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await refreshAll();
	}

	async function syncUsage() {
		if (selectedAgent !== "codex") {
			showNotice(translate("notice.usageSynced.title"), translate("notice.usageSynced.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await runAction("usage-sync", async () => {
			usageSyncResult = await track("usage-sync", UsageService.SyncCodex());
			await refreshUsage();
			showNotice(translate("notice.usageSynced.title"), translate("notice.usageSynced.codexDescription"));
		});
	}

	async function runHealth() {
		if (selectedAgent !== "codex") {
			showNotice(translate("notice.healthOK.title"), translate("notice.healthOK.placeholderDescription", { agent: currentAgent.name }));
			return;
		}
		await runAction("doctor", async () => {
			doctorResult = await track("doctor", DoctorService.Run());
			showNotice(translate("notice.healthOK.title"), translate("notice.healthOK.codexDescription"));
		});
	}

	async function repairLock() {
		if (selectedAgent !== "codex") {
			showNotice(translate("notice.lockOK.title"), translate("notice.lockOK.noRepair"));
			return;
		}
		await runAction("repair-lock", async () => {
			await track("repair-lock", DoctorService.RepairLock(true));
			showNotice(translate("notice.lockOK.title"), translate("notice.lockOK.repaired"));
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

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		cancelAction(key);
		inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => {
			if (inFlight.get(key) === promise) inFlight.delete(key);
		}).catch(() => {});
		return promise;
	}

	function cancelAction(key: string) {
		inFlight.get(key)?.cancel("replaced");
		inFlight.delete(key);
	}

	function cancelDetect() {
		cancelAction("detect");
	}

	function cancelAll() {
		for (const promise of inFlight.values()) promise.cancel("unmount");
		inFlight.clear();
	}

	function handleDashboardUpdate(payload: DashboardUpdatePayload | null | undefined) {
		if (!payload) return;
		if (payload.dashboard) applyDashboardResult(payload.dashboard);
		if (payload.error && !isCancelError(payload.error)) showError(payload.error);
		if (payload.event?.error && !isCancelError(payload.event.error)) showError(payload.event.error);
		if (payload.event?.kind === desktopChangeKind.codexProfileChanged) void refreshDetect();
		updateRefreshSummary();
	}

	function applyDashboardResult(next: DashboardResult) {
		dashboard = next;
		if (next.doctor) doctorResult = next.doctor;
		if (next.codex_profiles?.profiles) {
			codexProfileSummaries = next.codex_profiles.profiles;
			profileError = "";
		}
		if (next.usage) {
			usageSummary = next.usage;
			usageError = "";
		}
		updateRefreshSummary();
	}

	function buildCodexAgent(): AgentData {
		return {
			id: "codex",
			name: "Codex",
			detected: codexDetected(),
			current: activeCodexProfileID(),
			profiles: codexProfileSummaries.map((summary) => ({
				id: summary.profile.id,
				name: summary.profile.name || summary.profile.id,
				description: summary.profile.description || translate("profile.noDescription"),
				model: summary.model || "",
				provider: summary.model_provider || "",
				account: summary.codex_account_id || "",
			})),
			usage: codexUsage(),
			health: codexHealth(),
			placeholder: false,
		};
	}

	function agentByID(agentID: AgentID): AgentData {
		return agentID === "codex" ? codexAgent : placeholderAgents[agentID];
	}

	function activeCodexProfileID(): string {
		return dashboard?.active_states?.find((state) => state.provider_id === codexProviderID)?.profile_id ?? "";
	}

	function codexDetected(): boolean {
		if (detectResult) {
			return detectResult.codex_dir_exists || detectResult.config_status !== "missing" || detectResult.auth_status !== "missing" || detectResult.provider_exists;
		}
		return codexProfileSummaries.length > 0 || (dashboard?.providers?.some((provider) => provider.id === codexProviderID) ?? false);
	}

	function codexUsage() {
		return {
			events: usageSummary ? formatInteger(usageSummary.event_count) : "—",
			input: usageSummary ? formatCompact(usageSummary.input_tokens) : "—",
			output: usageSummary ? formatCompact(usageSummary.output_tokens) : "—",
			cost: usageSummary?.estimated_cost_usd ? formatCurrency(usageSummary.estimated_cost_usd) : "—",
		};
	}

	function codexHealth() {
		return {
			overall: doctorResult?.overall_level ? doctorResult.overall_level.toUpperCase() : "—",
			lock: doctorResult?.lock?.level ? doctorResult.lock.level.toUpperCase() : "—",
			pending: dashboard?.status.pending_operations ?? 0,
			failed: dashboard?.status.failed_operations ?? 0,
			findings: doctorResult?.findings?.map((finding) => ({ id: finding.id, status: finding.level.toUpperCase(), message: finding.message })) ?? [],
		};
	}

	function updateRefreshSummary() {
		const detectStatus = detectResult ? `${detectResult.config_status}/${detectResult.auth_status}` : detectError ? translate("status.error") : translate("status.pending");
		lastRefreshSummary = translate("diagnostics.summary", {
			dashboard: dashboard ? translate("status.loaded") : translate("status.empty"),
			detect: detectStatus,
			profiles: codexProfileSummaries.length,
		});
	}

	function diagnosticItems(currentLocale?: string | null): string[] {
		void currentLocale;
		const items = [lastRefreshSummary];
		if (dashboardError) items.push(translate("diagnostics.dashboardError", { message: dashboardError }));
		if (detectError) items.push(translate("diagnostics.detectError", { message: detectError }));
		if (profileError) items.push(translate("diagnostics.profilesError", { message: profileError }));
		if (usageError) items.push(translate("diagnostics.usageError", { message: usageError }));
		return items;
	}

	function showDiagnostics(): boolean {
		return selectedAgent === "codex" && (!currentAgent.detected || currentAgent.profiles.length === 0 || !!dashboardError || !!detectError || !!profileError || !!usageError);
	}

	function switchPlaceholderProfile(agentID: PlaceholderAgentID, profileID: string) {
		placeholderAgents = { ...placeholderAgents, [agentID]: { ...placeholderAgents[agentID], current: profileID } };
		showNotice(
			translate("notice.profileSwitched.title"),
			translate("notice.profileSwitched.placeholderDescription", { agent: placeholderAgents[agentID].name, profile: profileID }),
		);
	}

	function parseWorkspaceRoute(path: string): WorkspaceRoute {
		const list = (): WorkspaceRoute => ({ view: "profiles", profile: { kind: "list", profileID: "" }, valid: true });
		if (path === "/" || path === "/codex/profiles") return list();
		if (path === "/codex/profiles/new") return { view: "profiles", profile: { kind: "new", profileID: "" }, valid: true };
		const fork = path.match(/^\/codex\/profiles\/([^/]+)\/fork$/);
		if (fork) {
			const profileID = decodeRouteID(fork[1]);
			return profileID ? { view: "profiles", profile: { kind: "fork", profileID }, valid: true } : { ...list(), valid: false };
		}
		const detail = path.match(/^\/codex\/profiles\/([^/]+)$/);
		if (detail) {
			const profileID = decodeRouteID(detail[1]);
			return profileID ? { view: "profiles", profile: { kind: "detail", profileID }, valid: true } : { ...list(), valid: false };
		}
		if (path === "/codex/health") return { view: "health", profile: { kind: "list", profileID: "" }, valid: true };
		if (path === "/codex/usage") return { view: "usage", profile: { kind: "list", profileID: "" }, valid: true };
		if (path === "/settings") return { view: "settings", profile: { kind: "list", profileID: "" }, valid: true };
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
		const hint = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform || navigator.platform || navigator.userAgent;
		const normalized = hint.toLowerCase();
		if (normalized.includes("mac")) return "macos";
		if (normalized.includes("win")) return "windows";
		return "linux";
	}

	function sidebarTopPadding(): string {
		return platform === "macos" ? "pt-[52px]" : "pt-3";
	}

	function formatInteger(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale()).format(value);
	}

	function formatCompact(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale(), { notation: "compact", maximumFractionDigits: 2 }).format(value);
	}

	function formatCurrency(value: string): string {
		const parsed = Number(value);
		if (!Number.isFinite(parsed)) return value;
		return new Intl.NumberFormat(currentDesktopLocale(), { style: "currency", currency: "USD" }).format(parsed);
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

	function statusVariant(value: string): StatusVariant {
		if (value === "applied" || value === "restored" || value === "OK") return "ok";
		if (value === "failed" || value === "ERROR" || value === "WARN") return "warn";
		return "muted";
	}

	function statusPillClass(variant: StatusVariant) {
		return cn(
			"h-auto rounded px-1.5 py-px text-xs leading-none",
			variant === "ok" && "border-success/25 bg-success/15 text-success",
			variant === "warn" && "border-warning/25 bg-warning/15 text-warning",
			variant === "muted" && "border-border bg-muted text-muted-foreground",
		);
	}
</script>

<div class="h-screen overflow-hidden bg-background text-foreground">
	<div class="grid h-full min-h-0 grid-cols-[188px_minmax(0,1fr)]">
		<aside class="drag-region flex min-h-0 flex-col border-r bg-muted/50">
			<div class={cn("px-3 pb-3", sidebarTopPadding())}>
				<div class="flex items-center gap-2 px-1">
					<div class="grid size-5 shrink-0 select-none place-items-center rounded-md bg-foreground text-[10px] font-bold text-background">P</div>
					<span class="text-sm font-semibold tracking-tight">ProfileDeck</span>
					<Badge class="ml-auto" variant="outline">{$_("app.dev")}</Badge>
				</div>
			</div>

			<nav class="no-drag flex min-h-0 flex-1 flex-col gap-1 overflow-auto px-2 pb-2">
				<div class="px-2 pb-1 text-xs font-medium text-muted-foreground">{$_("nav.agents")}</div>
				{#each agentOrder as agentID (agentID)}
					{@const agent = agentByID(agentID)}
					<button
						type="button"
						onclick={() => selectAgent(agentID)}
						class={cn(
							"flex w-full flex-col gap-1 rounded-md px-2.5 py-2 text-left transition-colors",
							selectedView !== "settings" && selectedAgent === agentID ? "bg-background shadow-sm ring-1 ring-border" : "hover:bg-accent",
						)}
					>
						<div class="flex items-center justify-between gap-2">
							<span class="text-sm font-medium">{agent.name}</span>
							<Badge variant={agent.detected ? "secondary" : "destructive"}>{agent.detected ? $_("status.ready") : $_("status.missing")}</Badge>
						</div>
						<div class="truncate font-mono text-xs text-muted-foreground">{agent.current || "—"}</div>
					</button>
				{/each}
			</nav>

			<div class="no-drag border-t p-2">
				<button
					type="button"
					onclick={() => navigateToView("settings")}
					class={cn("w-full rounded-md px-2.5 py-1.5 text-left text-xs hover:bg-accent", selectedView === "settings" ? "bg-background shadow-sm ring-1 ring-border" : "text-muted-foreground")}
				>
					{$_("nav.settings")}
				</button>
			</div>
		</aside>

		<main class="flex min-h-0 min-w-0 flex-col bg-background">
			<div class="drag-region flex h-[50px] shrink-0 items-center justify-between gap-3 border-b px-4">
				<div class="flex min-w-0 items-center gap-2">
					<h1 class="text-base font-semibold tracking-tight">{selectedView === "settings" ? $_("settings.title") : currentAgent.name}</h1>
					{#if selectedView !== "settings"}
						<Badge variant={currentAgent.detected ? "secondary" : "destructive"}>{currentAgent.detected ? $_("status.detected") : $_("status.notDetected")}</Badge>
						{#if currentAgent.current}<Badge variant="outline">{currentAgent.current}</Badge>{/if}
					{/if}
				</div>
				<div class="no-drag flex shrink-0 items-center gap-2">
					{#if selectedView !== "settings"}
						<Button size="sm" variant="outline" disabled={loading} onclick={refreshSelectedAgent}>
							{#if loading && selectedAgent === "codex"}<Spinner data-icon="inline-start" />{/if}
							{$_("actions.refresh")}
						</Button>
						<Button size="sm" variant="outline" disabled={actionBusy === "detect"} onclick={detectSelectedAgent}>
							{#if actionBusy === "detect"}<Spinner data-icon="inline-start" />{/if}
							{$_("actions.detect")}
						</Button>
					{/if}
					<Button size="icon-sm" variant="ghost" onclick={toggleTheme} aria-label={theme === "light" ? $_("app.themeToDark") : $_("app.themeToLight")}>
						{theme === "light" ? "☾" : "☀"}
					</Button>
				</div>
			</div>

			{#if selectedView !== "settings" && showDiagnostics()}
				<div class="border-b bg-muted/40 px-4 py-2 text-xs text-muted-foreground">
					<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
						<span class="font-medium text-foreground">{$_("diagnostics.codexService")}</span>
						{#each diagnosticItems($locale) as item (item)}<span class="font-mono">{item}</span>{/each}
					</div>
				</div>
			{/if}

			{#if selectedView === "settings"}
				<div class="min-h-0 flex-1 overflow-auto p-4">{@render SettingsView()}</div>
			{:else}
				<Tabs.Root bind:value={selectedTab} class="flex min-h-0 flex-1 flex-col">
					<div class="shrink-0 border-b px-4">
						<Tabs.List variant="line" class="h-auto bg-transparent p-0">
							{#each tabs as tab (tab.id)}
								<Tabs.Trigger value={tab.id} onclick={() => navigateToView(tab.id)}>{$_(tab.labelKey)}</Tabs.Trigger>
							{/each}
						</Tabs.List>
					</div>
					<div class="min-h-0 flex-1 overflow-auto p-4">
						<Tabs.Content value="profiles" class="m-0">
							{#if selectedAgent === "codex"}
								<CodexProfiles
									route={workspaceRoute.profile}
									profiles={codexProfileSummaries}
									{detectResult}
									activeProfileID={activeCodexProfileID()}
									{loadingProfiles}
									{profileError}
									{useRequest}
									refreshDetect={refreshDetect}
									refreshProfiles={refreshCodexProfiles}
									{cancelDetect}
									onUseRequestHandled={(sequence) => { if (useRequest?.sequence === sequence) useRequest = null; }}
									{showError}
									{showNotice}
								/>
							{:else}
								{@render PlaceholderProfiles(currentAgent)}
							{/if}
						</Tabs.Content>
						<Tabs.Content value="health" class="m-0">{@render HealthView(currentAgent)}</Tabs.Content>
						<Tabs.Content value="usage" class="m-0">{@render UsageView(currentAgent)}</Tabs.Content>
					</div>
				</Tabs.Root>
			{/if}
		</main>
	</div>
</div>

{#snippet SettingsView()}
	<div class="mx-auto max-w-3xl overflow-hidden rounded-lg border bg-card">
		<div class="border-b px-4 py-3">
			<div class="text-sm font-semibold">{$_("settings.title")}</div>
			<div class="mt-1 text-xs text-muted-foreground">{$_("settings.description")}</div>
		</div>
		<Field.FieldGroup class="px-4 py-4">
			<Field.Field>
				<Field.FieldLabel for="desktop-language">{$_("settings.language.label")}</Field.FieldLabel>
				<div class="flex items-center gap-2">
					<Select.Root type="single" value={languagePreference} onValueChange={changeLanguage}>
						<Select.Trigger id="desktop-language" disabled={languageBusy}>{languagePreference === "zh-CN" ? $_("settings.language.zhCN") : languagePreference === "en-US" ? $_("settings.language.enUS") : $_("settings.language.auto")}</Select.Trigger>
						<Select.Content>
							<Select.Group>
								<Select.Item value="auto" label={$_("settings.language.auto")} />
								<Select.Item value="zh-CN" label={$_("settings.language.zhCN")} />
								<Select.Item value="en-US" label={$_("settings.language.enUS")} />
							</Select.Group>
						</Select.Content>
					</Select.Root>
					{#if languageBusy}<Spinner />{/if}
				</div>
				<Field.FieldDescription>{$_("settings.language.description")}</Field.FieldDescription>
			</Field.Field>
		</Field.FieldGroup>
	</div>
{/snippet}

{#snippet PlaceholderProfiles(agent: AgentData)}
	<div class="overflow-hidden rounded-lg border bg-card">
		{#if !agent.detected}
			{@render EmptyState($_("empty.agentNotDetectedTitle"), $_("empty.agentNotDetectedDescription", { values: { agent: agent.name } }))}
		{:else if agent.profiles.length === 0}
			{@render EmptyState($_("empty.noProfilesTitle"), $_("empty.noProfilesDescription", { values: { agent: agent.name } }))}
		{:else}
			{#each agent.profiles as profile, index (profile.id)}
				<div class={cn("flex items-center gap-4 px-4 py-3", index < agent.profiles.length - 1 && "border-b")}>
					<div class="flex min-w-0 flex-1 flex-col gap-1">
						<div class="flex items-center gap-2"><span class="truncate text-sm font-medium">{profile.name}</span>{#if profile.id === agent.current}<Badge variant="secondary">{$_("status.active")}</Badge>{/if}</div>
						<div class="truncate text-xs text-muted-foreground">{profile.description}</div>
						<div class="truncate text-xs text-muted-foreground">{profile.provider} · {profile.model}</div>
					</div>
					{#if profile.id !== agent.current}<Button size="sm" onclick={() => switchPlaceholderProfile(agent.id as PlaceholderAgentID, profile.id)}>{$_("actions.useProfile")}</Button>{/if}
				</div>
			{/each}
		{/if}
	</div>
{/snippet}

{#snippet EmptyState(title: string, description: string)}
	<Empty.Root class="border-0 py-12">
		<Empty.Header><Empty.Title>{title}</Empty.Title><Empty.Description>{description}</Empty.Description></Empty.Header>
	</Empty.Root>
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
			<div class="flex justify-end gap-2 border-b px-3 py-2">
				<Button size="sm" variant="outline" disabled={actionBusy === "doctor"} onclick={runHealth}>{#if actionBusy === "doctor"}<Spinner data-icon="inline-start" />{/if}{$_("actions.checkHealth")}</Button>
				<Button size="sm" variant="outline" disabled={actionBusy === "repair-lock"} onclick={repairLock}>{#if actionBusy === "repair-lock"}<Spinner data-icon="inline-start" />{/if}{$_("actions.repairLock")}</Button>
			</div>
			{#if agent.health.findings.length === 0 && agent.health.pending === 0 && agent.health.failed === 0}
				{@render EmptyState($_("empty.allChecksPassedTitle"), $_("empty.allChecksPassedDescription"))}
			{:else}
				<Table.Root>
					<Table.Header><Table.Row><Table.Head>{$_("health.finding")}</Table.Head><Table.Head>{$_("health.status")}</Table.Head><Table.Head>{$_("health.message")}</Table.Head></Table.Row></Table.Header>
					<Table.Body>
						{#each agent.health.findings as finding (finding.id)}
							<Table.Row><Table.Cell class="font-mono text-xs">{finding.id}</Table.Cell><Table.Cell><Badge variant="outline" class={statusPillClass(statusVariant(finding.status))}>{finding.status}</Badge></Table.Cell><Table.Cell class="text-xs text-muted-foreground">{finding.message}</Table.Cell></Table.Row>
						{/each}
					</Table.Body>
				</Table.Root>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet UsageView(agent: AgentData)}
	<div class="flex flex-col gap-3">
		<div class="flex justify-end"><Button size="sm" variant="outline" disabled={actionBusy === "usage-sync"} onclick={syncUsage}>{#if actionBusy === "usage-sync"}<Spinner data-icon="inline-start" />{/if}{$_("actions.sync")}</Button></div>
		<div class="grid grid-cols-2 gap-3">
			{@render Metric($_("usage.events"), agent.usage.events)}
			{@render Metric($_("usage.inputTokens"), agent.usage.input)}
			{@render Metric($_("usage.outputTokens"), agent.usage.output)}
			{@render Metric($_("usage.cost"), agent.usage.cost, agent.usage.cost === "—" ? $_("usage.configurePricing") : undefined)}
		</div>
		{#if selectedAgent === "codex" && usageSyncResult?.errors?.length}
			<div class="rounded-lg border bg-card px-3 py-2 text-xs text-muted-foreground">{$_("usage.importErrors", { values: { count: usageSyncResult.errors.length } })}</div>
		{/if}
	</div>
{/snippet}

{#snippet Metric(label: string, value: string, sub?: string)}
	<div class="flex min-w-0 flex-col gap-1 rounded-lg border bg-card p-3">
		<div class="text-xs font-medium text-muted-foreground">{label}</div>
		<div class="truncate text-xl font-semibold">{value}</div>
		{#if sub}<div class="text-xs text-muted-foreground">{sub}</div>{/if}
	</div>
{/snippet}
