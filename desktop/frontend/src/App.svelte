<script lang="ts">
	import { onMount } from "svelte";
	import { CancelError, Events, type CancellablePromise } from "@wailsio/runtime";
	import { setMode } from "mode-watcher";
	import { toast } from "svelte-sonner";

	import {
		AppService,
		BackupService,
		CodexService,
		DoctorService,
		SwitchService,
		UsageService,
	} from "../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexProfileCaptureRequest,
		DashboardResult,
		DesktopError,
	} from "../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		BackupSummary,
		CodexDetectResult,
		CodexProfileSummary,
		DoctorResult,
		SwitchPlan,
		UsageSummaryResult,
		UsageSyncResult,
	} from "../bindings/github.com/strahe/profiledeck/internal/app/models";

	import * as Dialog from "$lib/components/ui/dialog";
	import * as Empty from "$lib/components/ui/empty";
	import * as Field from "$lib/components/ui/field";
	import * as Table from "$lib/components/ui/table";
	import * as Tabs from "$lib/components/ui/tabs";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Spinner } from "$lib/components/ui/spinner";
	import { Toaster } from "$lib/components/ui/sonner";
	import { cn } from "$lib/utils";

	type AgentID = "codex" | "claude" | "gemini" | "opencode";
	type PlaceholderAgentID = Exclude<AgentID, "codex">;
	type WorkspaceTab = "profiles" | "backups" | "health" | "usage";
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

	type AgentProfile = {
		id: string;
		name: string;
		desc: string;
		updated: string;
		model: string;
		provider: string;
		summary?: CodexProfileSummary;
	};

	type AgentBackup = {
		id: string;
		profile: string;
		created: string;
		status: "applied" | "created" | "restored" | "failed";
		backup?: BackupSummary;
	};

	type AgentData = {
		id: AgentID;
		name: string;
		detected: boolean;
		current: string;
		targets: string[];
		profiles: AgentProfile[];
		backups: AgentBackup[];
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

	const codexProviderID = "codex";
	const tabs: Array<{ id: WorkspaceTab; label: string }> = [
		{ id: "profiles", label: "Profiles" },
		{ id: "backups", label: "Backups" },
		{ id: "health", label: "Health" },
		{ id: "usage", label: "Usage" },
	];
	const agentOrder: AgentID[] = ["codex", "claude", "gemini", "opencode"];
	const desktopChangeKind = {
		codexProfileCaptured: "codex-profile-captured",
		switchApplied: "switch-applied",
		rollbackApplied: "rollback-applied",
		switchRecovered: "switch-recovered",
		usageSynced: "usage-synced",
	} as const;

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
				},
				{
					id: "client-a",
					name: "client-a",
					desc: "Client A workspace",
					updated: "Yesterday 19:42",
					model: "claude-sonnet-4-6",
					provider: "anthropic",
				},
			],
			backups: [
				{
					id: "switch-178330112",
					profile: "personal",
					created: "2026-07-06 20:18:41",
					status: "applied",
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
			backups: [],
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
			backups: [],
			usage: { events: "—", input: "—", output: "—", cost: "—" },
			health: { overall: "—", lock: "—", pending: 0, failed: 0, findings: [] },
			placeholder: true,
		},
	};

	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let selectedAgent: AgentID = "codex";
	let selectedTab: WorkspaceTab = "profiles";
	let theme: "light" | "dark" = "light";
	let platform: Platform = detectPlatform();
	let loading = false;
	let loadingProfiles = false;
	let actionBusy = "";
	let lastToast = "";

	let dashboard: DashboardResult | null = null;
	let detectResult: CodexDetectResult | null = null;
	let doctorResult: DoctorResult | null = null;
	let backups: BackupSummary[] = [];
	let usageSummary: UsageSummaryResult | null = null;
	let usageSyncResult: UsageSyncResult | null = null;
	let codexProfileSummaries: CodexProfileSummary[] = [];
	let profileError = "";

	let placeholderAgents: Record<PlaceholderAgentID, AgentData> = initialPlaceholders;

	let captureOpen = false;
	let captureProfileID = "";
	let captureProfileName = "";
	let captureDescription = "";

	let useConfirmOpen = false;
	let pendingUse: PendingUse | null = null;

	$: codexBackups = backups.filter((backup) => backup.provider_id === codexProviderID);
	$: codexAgent = buildCodexAgent();
	$: currentAgent = agentByID(selectedAgent);

	onMount(() => {
		applyTheme(theme);
		void refreshAll();

		const off = [
			Events.On("profiledeck:open-switch", (event) => {
				const payload = event.data ?? {};
				if (payload.provider_id !== codexProviderID || !payload.profile_id) return;
				selectedAgent = "codex";
				selectedTab = "profiles";
				void openUseByProfileID(payload.profile_id);
			}),
			Events.On("profiledeck:open-doctor", () => {
				selectedAgent = "codex";
				selectedTab = "health";
				void runHealth();
			}),
			Events.On("profiledeck:usage-synced", (event) => {
				usageSyncResult = event.data ?? null;
				showNotice("Usage synced", "Codex usage logs were parsed.");
				void refreshUsage();
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
		try {
			const nextDashboard = await track("dashboard", AppService.Dashboard());
			dashboard = nextDashboard;
			if (nextDashboard.doctor) doctorResult = nextDashboard.doctor;
			await Promise.allSettled([
				refreshDetect(),
				refreshCodexProfiles(),
				refreshBackups(),
				refreshUsage(),
			]);
		} catch (err) {
			if (!isCancelError(err)) showError(err);
		} finally {
			loading = false;
		}
	}

	async function refreshDetect() {
		try {
			detectResult = await track("detect", CodexService.Detect());
		} catch (err) {
			if (!isCancelError(err)) detectResult = null;
		}
	}

	async function refreshCodexProfiles() {
		loadingProfiles = true;
		profileError = "";
		try {
			const result = await track("codex-profiles", CodexService.ListProfiles());
			codexProfileSummaries = result.profiles ?? [];
		} catch (err) {
			if (!isCancelError(err)) {
				codexProfileSummaries = [];
				profileError = formatError(err);
			}
		} finally {
			loadingProfiles = false;
		}
	}

	async function refreshBackups() {
		try {
			backups = (await track("backups", BackupService.ListBackups())).backups ?? [];
		} catch (err) {
			if (!isCancelError(err)) backups = [];
		}
	}

	async function refreshUsage() {
		try {
			usageSummary = await track("usage-summary", UsageService.Summary(codexProviderID));
		} catch (err) {
			if (!isCancelError(err)) usageSummary = null;
		}
	}

	async function detectSelectedAgent() {
		if (selectedAgent !== "codex") {
			showNotice("Detected", `${currentAgent.name} placeholder paths verified.`);
			return;
		}
		await runAction("detect", async () => {
			detectResult = await track("detect", CodexService.Detect());
			showNotice("Detected", "Codex paths verified.");
		});
	}

	async function refreshSelectedAgent() {
		if (selectedAgent !== "codex") {
			showNotice("Refreshed", `${currentAgent.name} placeholder state is up to date.`);
			return;
		}
		await refreshAll();
	}

	function openCapture(profile?: AgentProfile) {
		captureProfileID = profile?.id ?? "";
		captureProfileName = profile?.name ?? "";
		captureDescription = profile?.desc ?? "";
		captureOpen = true;
	}

	async function saveCurrentProfile() {
		if (selectedAgent !== "codex") {
			savePlaceholderProfile(selectedAgent, captureProfileID, captureProfileName, captureDescription);
			captureOpen = false;
			showNotice("Snapshot saved", `${currentAgent.name} config saved as a reusable profile.`);
			return;
		}
		await runAction("save-current", async () => {
			const req: CodexProfileCaptureRequest = {
				profile_id: captureProfileID,
				account_id: "",
				name: optional(captureProfileName),
				description: optional(captureDescription),
			};
			const result = await track("save-current", CodexService.CaptureProfile(req));
			captureOpen = false;
			await refreshCodexProfiles();
			showNotice("Snapshot saved", `Codex config saved as ${result.profile.name || result.profile.id}.`);
		});
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
			showNotice("Profile switched", `${agentName} now uses "${pendingUse.profileID}". Restart to take effect.`);
			pendingUse = null;
			return;
		}
		if (!pendingUse.plan?.plan_fingerprint) {
			showError("The selected profile is not ready to use.");
			return;
		}
		if (unsupportedOperationCount(pendingUse.plan) > 0) {
			showError("This profile cannot be used until unsupported target changes are resolved.");
			return;
		}
		await runAction("use-apply", async () => {
			const current = pendingUse;
			if (!current?.plan) return;
			const result = await track(
				"use-apply",
				SwitchService.Apply({
					provider_id: codexProviderID,
					profile_id: current.profileID,
					expected_plan_fingerprint: current.plan.plan_fingerprint,
					confirm: true,
				}),
			);
			useConfirmOpen = false;
			pendingUse = null;
			await Promise.allSettled([refreshCodexProfiles(), refreshBackups()]);
			showNotice("Profile switched", `Codex now uses ${result.profile.name || result.profile.id}. Restart to take effect.`);
		});
	}

	async function restoreBackup(backup: AgentBackup) {
		if (selectedAgent !== "codex") {
			showNotice("Backup restored", `Backup ${backup.id} restored. Restart ${currentAgent.name}.`);
			return;
		}
		if (!backup.backup?.rollback_supported) {
			showError(backup.backup?.unsupported_reason || "This backup cannot be restored.");
			return;
		}
		await runAction("restore", async () => {
			const result = await track("restore", BackupService.ApplyRollback(backup.id, true));
			showNotice("Backup restored", `Backup restored: ${result.operation_id}. Restart Codex to load it.`);
		});
	}

	async function syncUsage() {
		if (selectedAgent !== "codex") {
			showNotice("Usage synced", `${currentAgent.name} placeholder usage logs were parsed.`);
			return;
		}
		await runAction("usage-sync", async () => {
			usageSyncResult = await track("usage-sync", UsageService.SyncCodex());
			await refreshUsage();
			showNotice("Usage synced", "Codex usage logs were parsed.");
		});
	}

	async function runHealth() {
		if (selectedAgent !== "codex") {
			showNotice("Health OK", `No incomplete ${currentAgent.name} operations found.`);
			return;
		}
		await runAction("doctor", async () => {
			doctorResult = await track("doctor", DoctorService.Run());
			showNotice("Health OK", "Doctor check finished.");
		});
	}

	async function repairLock() {
		if (selectedAgent !== "codex") {
			showNotice("Lock OK", "No repair was necessary.");
			return;
		}
		await runAction("repair-lock", async () => {
			await track("repair-lock", DoctorService.RepairLock(true));
			showNotice("Lock OK", "Lock repair finished.");
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
			dashboard = payload.dashboard;
			if (payload.dashboard.doctor) doctorResult = payload.dashboard.doctor;
		}
		if (payload.error && !isCancelError(payload.error)) showError(payload.error);
		if (payload.event?.error && !isCancelError(payload.event.error)) showError(payload.event.error);
		void refreshLightForEvent(payload.event);
	}

	async function refreshLightForEvent(event: DesktopChangeEvent | null | undefined) {
		if (!event) return;
		if (
			event.kind === desktopChangeKind.switchApplied ||
			event.kind === desktopChangeKind.rollbackApplied ||
			event.kind === desktopChangeKind.switchRecovered
		) {
			await Promise.allSettled([refreshBackups(), refreshCodexProfiles()]);
		}
		if (event.kind === desktopChangeKind.usageSynced) await refreshUsage();
		if (event.kind === desktopChangeKind.codexProfileCaptured) await refreshCodexProfiles();
	}

	function buildCodexAgent(): AgentData {
		const profiles = codexProfileSummaries ?? [];
		const operationBackups = codexBackups ?? [];
		return {
			id: "codex",
			name: "Codex",
			detected: codexDetected(),
			current: activeCodexProfileID(),
			targets: codexTargets(),
			profiles: profiles.map(codexProfileFromSummary),
			backups: operationBackups.map(codexBackupFromSummary),
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
			desc: summary.profile.description || "No description",
			updated: formatRelativeTime(summary.updated_at_unix_ms),
			model: summary.model || "—",
			provider: summary.model_provider || "—",
			summary,
		};
	}

	function codexBackupFromSummary(backup: BackupSummary): AgentBackup {
		return {
			id: backup.backup_id,
			profile: backup.profile_id,
			created: formatDateTime(backup.created_at_unix_ms),
			status: backup.operation_status === "failed" ? "failed" : backup.operation_status === "rolled_back" ? "restored" : "applied",
			backup,
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

	function savePlaceholderProfile(agentID: AgentID, profileID: string, name: string, description: string) {
		if (agentID === "codex") return;
		const id = profileID.trim() || "work";
		const profile: AgentProfile = {
			id,
			name: name.trim() || id,
			desc: description.trim() || "Saved placeholder config",
			updated: "Just now",
			model: placeholderAgents[agentID].profiles[0]?.model ?? "—",
			provider: placeholderAgents[agentID].profiles[0]?.provider ?? "—",
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
		if (delta < 60_000) return "Just now";
		if (delta < 3_600_000) return `${Math.max(1, Math.floor(delta / 60_000))}m ago`;
		if (delta < 86_400_000) return `Today ${new Date(value).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}`;
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
		if (typeof value === "string") return value.trim() === "{}" ? "Desktop services are unavailable." : value;
		if (Array.isArray(value)) return value.map(formatError).filter(Boolean).join("\n");
		const typed = value as DesktopError & { message?: string; code?: string; name?: string; cause?: unknown };
		if (typed.cause && typed.cause !== value) {
			const causeMessage = formatError(typed.cause);
			if (causeMessage) return causeMessage;
		}
		if (typed.code && typed.message) return `${typed.code}: ${typed.message}`;
		if (typed.message) return typed.message;
		try {
			const json = JSON.stringify(value);
			return json === "{}" ? "Desktop services are unavailable." : json;
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

	function showNotice(title: string, description: string) {
		const key = `${title}:${description}`;
		if (lastToast === key) return;
		lastToast = key;
		toast.success(title, { description });
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
</script>

<Toaster richColors position="top-right" />

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
						dev
					</span>
				</div>
			</div>

			<nav class="no-drag flex min-h-0 flex-1 flex-col gap-0.5 overflow-auto px-2 pb-2">
				<div class="px-2 pb-1.5 pt-0.5 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
					Agents
				</div>
				{#each agentOrder as agentID (agentID)}
					{@const agent = agentByID(agentID)}
					<button
						type="button"
						onclick={() => {
							selectedAgent = agentID;
							selectedTab = "profiles";
						}}
						class={cn(
							"flex w-full flex-col gap-0.5 rounded-md px-2.5 py-2 text-left transition-colors",
							selectedAgent === agentID
								? "bg-background text-foreground shadow-sm ring-1 ring-border"
								: "text-foreground hover:bg-accent",
						)}
					>
						<div class="flex items-center justify-between gap-1">
							<span class="text-sm font-medium">{agent.name}</span>
							<Badge variant="outline" class={statusPillClass(agent.detected ? "ok" : "warn")}>
								{agent.detected ? "Ready" : "Missing"}
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
					onclick={() => showNotice("Settings", "Data directory, pricing, and advanced diagnostics.")}
					class="rounded-md px-2.5 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
				>
					Settings
				</button>
			</div>
		</aside>

		<main class="flex min-h-0 min-w-0 flex-col bg-background">
			<div class="drag-region flex h-[50px] shrink-0 items-center justify-between gap-3 border-b px-4">
				<div class="flex min-w-0 items-center gap-2">
					<h1 class="text-base font-bold leading-none tracking-tight">{currentAgent.name}</h1>
					<Badge variant="outline" class={statusPillClass(currentAgent.detected ? "ok" : "warn")}>
						{currentAgent.detected ? "Detected" : "Not detected"}
					</Badge>
					{#if currentAgent.current}
						<Badge variant="outline" class={statusPillClass("muted")}>{currentAgent.current}</Badge>
					{/if}
				</div>
				<div class="no-drag flex shrink-0 items-center gap-2">
					<Button class={smallButtonClass()} size="sm" variant="outline" disabled={loading} onclick={refreshSelectedAgent}>
						{#if loading && selectedAgent === "codex"}
							<Spinner data-icon="inline-start" />
						{/if}
						Refresh
					</Button>
					<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "detect"} onclick={detectSelectedAgent}>
						{#if actionBusy === "detect"}
							<Spinner data-icon="inline-start" />
						{/if}
						Detect
					</Button>
					<Button class={smallButtonClass()} size="sm" onclick={() => openCapture()}>Save Current</Button>
					<div class="mx-0.5 h-4 w-px bg-border"></div>
					<button
						type="button"
						onclick={toggleTheme}
						title={theme === "light" ? "Switch to dark mode" : "Switch to light mode"}
						aria-label={theme === "light" ? "Switch to dark mode" : "Switch to light mode"}
						class="flex size-7 items-center justify-center rounded-md text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
					>
						{theme === "light" ? "☾" : "☀"}
					</button>
				</div>
			</div>

			<Tabs.Root bind:value={selectedTab} class="flex min-h-0 flex-1 flex-col">
				<div class="shrink-0 border-b px-4">
					<Tabs.List variant="line" class="h-auto gap-0 bg-transparent p-0">
						{#each tabs as tab (tab.id)}
							<Tabs.Trigger
								value={tab.id}
								class="h-auto rounded-none border-0 px-3 py-2.5 text-xs font-medium data-active:bg-transparent"
							>
								{tab.label}
							</Tabs.Trigger>
						{/each}
					</Tabs.List>
				</div>

				<div class="min-h-0 flex-1 overflow-auto p-4">
					<Tabs.Content value="profiles" class="m-0">
						{#if selectedAgent === "codex" && loadingProfiles}
							{@render EmptyState("Loading profiles", "Reading Codex profiles.")}
						{:else if selectedAgent === "codex" && profileError}
							{@render EmptyState("Unable to load profiles", profileError)}
						{:else}
							{@render ProfilesView(currentAgent)}
						{/if}
					</Tabs.Content>

					<Tabs.Content value="backups" class="m-0">
						{@render BackupsView(currentAgent)}
					</Tabs.Content>

					<Tabs.Content value="health" class="m-0">
						{@render HealthView(currentAgent)}
					</Tabs.Content>

					<Tabs.Content value="usage" class="m-0">
						{@render UsageView(currentAgent)}
					</Tabs.Content>
				</div>
			</Tabs.Root>
		</main>
	</div>
</div>

<Dialog.Root bind:open={useConfirmOpen}>
	<Dialog.Content class="gap-0 overflow-hidden p-0 sm:max-w-[400px]" showCloseButton={false}>
		<Dialog.Header class="gap-1 px-4 pb-3 pt-4">
			<Dialog.Title class="text-sm font-semibold">Use "{pendingUse?.profileName ?? pendingUse?.profileID}" for {agentByID(pendingUse?.agentID ?? selectedAgent).name}</Dialog.Title>
			<Dialog.Description class="text-xs leading-relaxed">
				Replaces {agentByID(pendingUse?.agentID ?? selectedAgent).name} config targets. A backup is created first. Restart {agentByID(pendingUse?.agentID ?? selectedAgent).name} after switching.
			</Dialog.Description>
		</Dialog.Header>
		<div class="border-t px-4 pb-2 pt-2.5 font-mono text-xs text-muted-foreground">
			{agentByID(pendingUse?.agentID ?? selectedAgent).current || "—"}
			<span class="mx-1.5 text-muted-foreground/50">→</span>
			{pendingUse?.profileID ?? "—"}
		</div>
		{#if pendingUse?.agentID === "codex" && unsupportedOperationCount(pendingUse.plan) > 0}
			<div class="mx-4 mb-3 rounded-md border border-warning/25 bg-warning/15 px-2.5 py-2 text-xs text-warning">
				This profile contains unsupported target changes.
			</div>
		{/if}
		<Dialog.Footer class="m-0 mx-0 mb-0 rounded-none border-t bg-transparent px-4 py-3">
			<Button class="rounded-md px-3" variant="outline" onclick={() => (useConfirmOpen = false)}>Cancel</Button>
			<Button
				class="rounded-md px-3"
				disabled={actionBusy === "use-apply" || (pendingUse?.agentID === "codex" && unsupportedOperationCount(pendingUse.plan) > 0)}
				onclick={confirmUse}
			>
				{#if actionBusy === "use-apply"}
					<Spinner data-icon="inline-start" />
				{/if}
				Use Profile
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={captureOpen}>
	<Dialog.Content class="gap-0 overflow-hidden p-0 sm:max-w-[400px]" showCloseButton={false}>
		<Dialog.Header class="gap-1 px-4 pb-3 pt-4">
			<Dialog.Title class="text-sm font-semibold">Save current config</Dialog.Title>
			<Dialog.Description class="text-xs">Snapshot the current {currentAgent.name} config on disk.</Dialog.Description>
		</Dialog.Header>
		<Field.FieldGroup class="border-t px-4 pb-3 pt-3">
			<Field.Field>
				<Field.FieldLabel for="capture-profile-id">Profile ID</Field.FieldLabel>
				<Input class="rounded-md border-border bg-background text-sm" id="capture-profile-id" bind:value={captureProfileID} placeholder="e.g. work" />
			</Field.Field>
			<Field.Field>
				<Field.FieldLabel for="capture-profile-name">Name</Field.FieldLabel>
				<Input class="rounded-md border-border bg-background text-sm" id="capture-profile-name" bind:value={captureProfileName} placeholder="e.g. Work" />
			</Field.Field>
			<Field.Field>
				<Field.FieldLabel for="capture-description">Description</Field.FieldLabel>
				<Input class="rounded-md border-border bg-background text-sm" id="capture-description" bind:value={captureDescription} placeholder="Optional" />
			</Field.Field>
		</Field.FieldGroup>
		<div class="mx-4 mb-3 rounded-md border bg-muted px-2.5 py-2">
			<div class="mb-1 text-xs font-medium text-muted-foreground">Targets</div>
			<div class="flex flex-col gap-0.5">
				{#each currentAgent.targets as target (target)}
					<div class="truncate font-mono text-xs">{target}</div>
				{/each}
			</div>
		</div>
		<Dialog.Footer class="m-0 mx-0 mb-0 rounded-none border-t bg-transparent px-4 py-3">
			<Button class="rounded-md px-3" variant="outline" onclick={() => (captureOpen = false)}>Cancel</Button>
			<Button class="rounded-md px-3" disabled={actionBusy === "save-current"} onclick={saveCurrentProfile}>
				{#if actionBusy === "save-current"}
					<Spinner data-icon="inline-start" />
				{/if}
				Save Snapshot
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

{#snippet ProfilesView(agent: AgentData)}
	{#if !agent.detected}
		{@render EmptyState("Agent not detected", `Install or configure ${agent.name} to manage profiles.`)}
	{:else if agent.profiles.length === 0}
		{@render EmptyState("No profiles yet", `Use "Save Current" to snapshot the current ${agent.name} config.`)}
	{:else}
		<div class="overflow-hidden rounded-lg border bg-card">
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
								<Badge variant="outline" class={statusPillClass("ok")}>Active</Badge>
							{/if}
						</div>
						<div class="truncate text-xs text-muted-foreground">{profile.desc}</div>
						<div class="truncate font-mono text-xs text-muted-foreground">
							{profile.provider} · {profile.model} · {profile.updated}
						</div>
					</div>
					<div class="flex shrink-0 items-center gap-1.5">
						{#if active}
							<Button class={smallButtonClass()} size="sm" variant="outline" onclick={() => openCapture(profile)}>Save Current</Button>
						{:else}
							<Button class={smallButtonClass()} size="sm" disabled={!!actionBusy} onclick={() => openUse(profile)}>Use</Button>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}
{/snippet}

{#snippet BackupsView(agent: AgentData)}
	<div class="overflow-hidden rounded-lg border bg-card">
		{#if agent.backups.length === 0}
			{@render EmptyState("No backups", "Backups are created automatically before each profile switch.")}
		{:else}
			<Table.Root>
				<Table.Header>
					<Table.Row>
						<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">ID</Table.Head>
						<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Profile</Table.Head>
						<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Status</Table.Head>
						<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Created</Table.Head>
						<Table.Head class="h-auto px-3 py-2 text-right text-xs font-semibold uppercase tracking-wide text-muted-foreground"></Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#each agent.backups as backup (backup.id)}
						<Table.Row class="last:border-0">
							<Table.Cell class="px-3 py-2.5 font-mono text-xs text-muted-foreground">{backup.id}</Table.Cell>
							<Table.Cell class="px-3 py-2.5 text-sm font-medium">{backup.profile}</Table.Cell>
							<Table.Cell class="px-3 py-2.5">
								<Badge variant="outline" class={statusPillClass(statusVariant(backup.status))}>{backup.status}</Badge>
							</Table.Cell>
							<Table.Cell class="px-3 py-2.5 text-xs tabular-nums text-muted-foreground">{backup.created}</Table.Cell>
							<Table.Cell class="px-3 py-2.5 text-right">
								<Button
									class={smallButtonClass()}
									size="sm"
									variant="outline"
									disabled={selectedAgent === "codex" && !backup.backup?.rollback_supported}
									onclick={() => restoreBackup(backup)}
								>
									Restore
								</Button>
							</Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
		{/if}
	</div>
{/snippet}

{#snippet HealthView(agent: AgentData)}
	<div class="flex flex-col gap-3">
		<div class="grid grid-cols-4 gap-3">
			{@render Metric("Overall", agent.health.overall)}
			{@render Metric("Lock", agent.health.lock)}
			{@render Metric("Pending", String(agent.health.pending))}
			{@render Metric("Failed", String(agent.health.failed))}
		</div>
		<div class="overflow-hidden rounded-lg border bg-card">
			<div class="flex items-center justify-end gap-2 border-b px-3 py-2">
				<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "doctor"} onclick={runHealth}>
					{#if actionBusy === "doctor"}
						<Spinner data-icon="inline-start" />
					{/if}
					Check Health
				</Button>
				<Button class={smallButtonClass()} size="sm" variant="outline" disabled={actionBusy === "repair-lock"} onclick={repairLock}>
					{#if actionBusy === "repair-lock"}
						<Spinner data-icon="inline-start" />
					{/if}
					Repair Lock
				</Button>
			</div>
			{#if agent.health.findings.length === 0 && agent.health.pending === 0 && agent.health.failed === 0}
				<Empty.Root class="min-h-0 gap-2 border-0 px-0 py-16">
					<Empty.Header>
						<Empty.Media class="mb-0">
							<span class="text-3xl opacity-60">✓</span>
						</Empty.Media>
						<Empty.Title class="text-sm font-medium">All checks passed</Empty.Title>
						<Empty.Description class="text-xs leading-snug">No pending or failed operations.</Empty.Description>
					</Empty.Header>
				</Empty.Root>
			{:else}
				<Table.Root>
					<Table.Header>
						<Table.Row>
							<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Finding</Table.Head>
							<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Status</Table.Head>
							<Table.Head class="h-auto px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Message</Table.Head>
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
				Sync
			</Button>
		</div>
		<div class="grid grid-cols-2 gap-3">
			{@render Metric("Events", agent.usage.events)}
			{@render Metric("Input tokens", agent.usage.input)}
			{@render Metric("Output tokens", agent.usage.output)}
			{@render Metric("Cost", agent.usage.cost, agent.usage.cost === "—" ? "Configure pricing to estimate" : undefined)}
		</div>
		{#if selectedAgent === "codex" && usageSyncResult?.errors?.length}
			<div class="rounded-lg border bg-card px-3 py-2 text-xs text-muted-foreground">
				{usageSyncResult.errors.length} usage import errors were skipped.
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
