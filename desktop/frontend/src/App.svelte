<script lang="ts">
	import { onMount } from "svelte";
	import { CancelError, Events, type CancellablePromise } from "@wailsio/runtime";
	import { toast } from "svelte-sonner";
	import ActivityIcon from "@lucide/svelte/icons/activity";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import CheckIcon from "@lucide/svelte/icons/check";
	import DatabaseIcon from "@lucide/svelte/icons/database";
	import LoaderIcon from "@lucide/svelte/icons/loader-2";
	import RefreshCcwIcon from "@lucide/svelte/icons/refresh-ccw";
	import RotateCcwIcon from "@lucide/svelte/icons/rotate-ccw";
	import ShieldIcon from "@lucide/svelte/icons/shield";
	import TerminalIcon from "@lucide/svelte/icons/terminal";
	import WalletCardsIcon from "@lucide/svelte/icons/wallet-cards";

	import {
		AppService,
		BackupService,
		CodexService,
		DoctorService,
		ProfileService,
		SwitchService,
		UsageService,
	} from "../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexProfileCaptureRequest,
		CodexProfileSetRequest,
		DashboardResult,
		DesktopError,
	} from "../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		BackupDetail,
		BackupSummary,
		CodexAccount,
		CodexDetectResult,
		DoctorResult,
		PlanOperation,
		Profile,
		ProfileTarget,
		SwitchPlan,
		UsageSummaryResult,
		UsageSyncResult,
	} from "../bindings/github.com/strahe/profiledeck/internal/app/models";

	import ConfirmAction from "$lib/components/app/ConfirmAction.svelte";
	import EmptyState from "$lib/components/app/EmptyState.svelte";
	import MetricCard from "$lib/components/app/MetricCard.svelte";
	import OperationTable from "$lib/components/app/OperationTable.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import * as Card from "$lib/components/ui/card";
	import * as Field from "$lib/components/ui/field";
	import { Input } from "$lib/components/ui/input";
	import * as Select from "$lib/components/ui/select";
	import { Separator } from "$lib/components/ui/separator";
	import { Spinner } from "$lib/components/ui/spinner";
	import * as Table from "$lib/components/ui/table";
	import * as Tabs from "$lib/components/ui/tabs";
	import { Toaster } from "$lib/components/ui/sonner";

	type Tab = "dashboard" | "codex" | "profiles" | "switch" | "recovery" | "usage" | "doctor";

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

	const codexProviderID = "codex";
	const desktopChangeStatus = {
		failure: "failure",
		canceled: "canceled",
	} as const;
	const desktopChangeKind = {
		codexProfileCaptured: "codex-profile-captured",
		codexProfileManaged: "codex-profile-managed",
		switchApplied: "switch-applied",
		rollbackApplied: "rollback-applied",
		switchRecovered: "switch-recovered",
		usageSynced: "usage-synced",
	} as const;

	const tabs: Array<{ id: Tab; label: string; icon: typeof ActivityIcon }> = [
		{ id: "dashboard", label: "Status", icon: ActivityIcon },
		{ id: "codex", label: "Codex", icon: TerminalIcon },
		{ id: "profiles", label: "Profiles", icon: WalletCardsIcon },
		{ id: "switch", label: "Switch", icon: RefreshCcwIcon },
		{ id: "recovery", label: "Recovery", icon: RotateCcwIcon },
		{ id: "usage", label: "Usage", icon: DatabaseIcon },
		{ id: "doctor", label: "Doctor", icon: ShieldIcon },
	];

	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let activeTab: Tab = "dashboard";
	let loading = false;
	let actionBusy = "";
	let error = "";
	let notice = "";
	let lastToast = "";

	let dashboard: DashboardResult | null = null;
	let detectResult: CodexDetectResult | null = null;
	let accounts: CodexAccount[] = [];
	let targets: ProfileTarget[] = [];
	let selectedProfileID = "";
	let switchPlan: SwitchPlan | null = null;
	let backups: BackupSummary[] = [];
	let selectedBackup: BackupDetail | null = null;
	let usageSummary: UsageSummaryResult | null = null;
	let usageSyncResult: UsageSyncResult | null = null;
	let doctorResult: DoctorResult | null = null;

	let codexProfileID = "";
	let codexAccountID = "";
	let codexProfileName = "";
	let codexProfileDescription = "";
	let managedModel = "gpt-5-codex";
	let managedModelProvider = "openai";
	let managedBaseURL = "";

	$: if (switchPlan && switchPlan.profile.id !== selectedProfileID) {
		switchPlan = null;
	}

	onMount(() => {
		void refreshAll();

		const off = [
			Events.On("profiledeck:open-switch", (event) => {
				const payload = event.data ?? {};
				if (payload.provider_id === codexProviderID && payload.profile_id) {
					void openSwitch(payload.profile_id);
				}
			}),
			Events.On("profiledeck:open-doctor", () => {
				activeTab = "doctor";
				void runDoctor();
			}),
			Events.On("profiledeck:usage-synced", (event) => {
				usageSyncResult = event.data ?? null;
				showNotice("Usage sync finished.");
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
		error = "";
		try {
			dashboard = await track("dashboard", AppService.Dashboard());
			if (dashboard.doctor) doctorResult = dashboard.doctor;
			await Promise.allSettled([refreshAccounts(), refreshBackups(), refreshUsage()]);
		} catch (err) {
			if (!isCancelError(err)) showError(err);
		} finally {
			loading = false;
		}
	}

	async function initialize() {
		await runAction("initialize", async () => {
			await track("initialize", AppService.Initialize());
			showNotice("Runtime initialized.");
		});
	}

	async function detectCodex() {
		await runAction("detect", async () => {
			detectResult = await track("detect", CodexService.Detect());
			showNotice("Codex detection finished.");
		});
	}

	async function refreshAccounts() {
		try {
			accounts = (await track("accounts", CodexService.ListAccounts())) ?? [];
		} catch (err) {
			if (!isCancelError(err)) accounts = [];
		}
	}

	async function captureCodexProfile() {
		await runAction("capture", async () => {
			const req: CodexProfileCaptureRequest = {
				profile_id: codexProfileID,
				account_id: codexAccountID,
				name: optional(codexProfileName),
				description: optional(codexProfileDescription),
			};
			const result = await track("capture", CodexService.CaptureProfile(req));
			setSelectedProfileID(result.profile.id);
			switchPlan = null;
			showNotice(`Captured ${result.profile.name || result.profile.id}.`);
		});
	}

	async function setManagedCodexProfile() {
		await runAction("managed", async () => {
			const req: CodexProfileSetRequest = {
				profile_id: codexProfileID,
				account_id: codexAccountID,
				name: optional(codexProfileName),
				description: optional(codexProfileDescription),
				model: managedModel,
				model_provider: managedModelProvider,
				openai_base_url: optional(managedBaseURL),
			};
			const result = await track("managed", CodexService.SetManagedProfile(req));
			setSelectedProfileID(result.profile.id);
			switchPlan = null;
			showNotice(`Saved ${result.profile.name || result.profile.id}.`);
		});
	}

	async function loadTargets(profileID: string) {
		setSelectedProfileID(profileID);
		targets = [];
		await runAction("targets", async () => {
			targets = (await track("targets", ProfileService.ListTargets(profileID, codexProviderID))) ?? [];
		});
	}

	async function openSwitch(profileID: string) {
		activeTab = "switch";
		setSelectedProfileID(profileID);
		switchPlan = null;
		await buildSwitchPlan();
	}

	async function buildSwitchPlan() {
		if (!selectedProfileID) {
			showError("Select a profile first.");
			return;
		}
		await runAction("plan", async () => {
			switchPlan = await track("plan", SwitchService.BuildPlan(codexProviderID, selectedProfileID));
		});
	}

	async function applySwitch() {
		if (!canApplyPlan(switchPlan)) {
			showError("Build a fresh switch plan before applying.");
			return;
		}
		await runAction("apply-switch", async () => {
			const plan = switchPlan;
			if (!plan) return;
			const result = await track(
				"apply-switch",
				SwitchService.Apply({
					provider_id: codexProviderID,
					profile_id: selectedProfileID,
					expected_plan_fingerprint: plan.plan_fingerprint,
					confirm: true,
				}),
			);
			switchPlan = null;
			showNotice(`Applied ${result.profile.name || result.profile.id}.`);
		});
	}

	async function refreshBackups() {
		try {
			backups = (await track("backups", BackupService.ListBackups())).backups ?? [];
		} catch (err) {
			if (!isCancelError(err)) backups = [];
		}
	}

	async function showBackup(backupID: string) {
		await runAction("show-backup", async () => {
			selectedBackup = await track("show-backup", BackupService.ShowBackup(backupID));
		});
	}

	async function applyRollback(backupID: string) {
		await runAction("rollback", async () => {
			const result = await track("rollback", BackupService.ApplyRollback(backupID, true));
			showNotice(`Rollback applied: ${result.operation_id}.`);
		});
	}

	async function recoverFailedSwitch(operationID: string) {
		await runAction("recover", async () => {
			const result = await track("recover", BackupService.RecoverFailedSwitch(operationID, true));
			showNotice(`Recovery applied: ${result.operation_id}.`);
		});
	}

	async function refreshUsage() {
		try {
			usageSummary = await track("usage-summary", UsageService.Summary(codexProviderID));
		} catch (err) {
			if (!isCancelError(err)) usageSummary = null;
		}
	}

	async function syncUsage() {
		await runAction("usage-sync", async () => {
			usageSyncResult = await track("usage-sync", UsageService.SyncCodex());
			await refreshUsage();
			showNotice("Usage sync finished.");
		});
	}

	async function runDoctor() {
		await runAction("doctor", async () => {
			doctorResult = await track("doctor", DoctorService.Run());
		});
	}

	async function repairLock() {
		await runAction("repair-lock", async () => {
			await track("repair-lock", DoctorService.RepairLock(true));
			showNotice("Lock repaired.");
		});
	}

	async function runAction(name: string, fn: () => Promise<void>) {
		if (actionBusy && actionBusy !== name) cancelAction(actionBusy);
		actionBusy = name;
		error = "";
		notice = "";
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

	function cancelCurrentAction() {
		if (actionBusy) cancelAction(actionBusy);
		if (loading) cancelAction("dashboard");
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
		if (
			payload.event?.status === desktopChangeStatus.failure &&
			payload.event.error &&
			!isCancelError(payload.event.error)
		) {
			showError(payload.event.error);
		}
		void refreshLightForEvent(payload.event);
	}

	async function refreshLightForEvent(event: DesktopChangeEvent | null | undefined) {
		if (!event) return;
		if (
			event.kind === desktopChangeKind.switchApplied ||
			event.kind === desktopChangeKind.rollbackApplied ||
			event.kind === desktopChangeKind.switchRecovered
		) {
			await refreshBackups();
		}
		if (event.kind === desktopChangeKind.usageSynced) await refreshUsage();
		if (
			event.kind === desktopChangeKind.codexProfileCaptured ||
			event.kind === desktopChangeKind.codexProfileManaged
		) {
			await refreshAccounts();
		}
	}

	function profiles(): Profile[] {
		return dashboard?.profiles ?? [];
	}

	function setSelectedProfileID(profileID: string) {
		selectedProfileID = profileID;
	}

	function activeCodexState() {
		return dashboard?.active_states?.find((state) => state.provider_id === codexProviderID);
	}

	function activeCodexProfileID(): string {
		const state = activeCodexState();
		return state?.profile_available ? (state.profile_id ?? "") : "";
	}

	function activeCodexProfileLabel(): string {
		const state = activeCodexState();
		if (!state?.profile_id) return "none";
		if (!state.profile_available) return `missing: ${state.profile_id}`;
		return state.profile_name || state.profile_id;
	}

	function missingActiveCodexProfileID(): string {
		const state = activeCodexState();
		return state?.profile_id && !state.profile_available ? state.profile_id : "";
	}

	function selectedProfile(): Profile | undefined {
		return profiles().find((profile) => profile.id === selectedProfileID);
	}

	function selectedProfileLabel(): string {
		const profile = selectedProfile();
		return profile?.name || profile?.id || "Select profile";
	}

	function planCounts(plan: SwitchPlan | null) {
		const counts = { create: 0, update: 0, noop: 0, unsupported: 0 };
		for (const op of plan?.operations ?? []) {
			if (op.action === "create") counts.create += 1;
			else if (op.action === "update") counts.update += 1;
			else if (op.action === "noop") counts.noop += 1;
			else counts.unsupported += 1;
		}
		return counts;
	}

	function canApplyPlan(plan: SwitchPlan | null): boolean {
		return (
			!!plan &&
			!!selectedProfileID &&
			plan.profile.id === selectedProfileID &&
			!!plan.plan_fingerprint &&
			(plan.operations ?? []).every((op) => op.action !== "unsupported")
		);
	}

	function operationPreview(op: PlanOperation): string {
		const before = op.before_preview?.content || "<empty>";
		const after = op.after_preview?.content || op.desired_preview?.content || "<empty>";
		return `Before\n${before}\n\nAfter\n${after}`;
	}

	function optional(value: string): string | null {
		const trimmed = value.trim();
		return trimmed === "" ? null : trimmed;
	}

	function costLabel(summary: UsageSummaryResult | null): string {
		if (!summary) return "unknown";
		if (summary.estimated_cost_usd) return `$${summary.estimated_cost_usd}`;
		return summary.cost_status || "unknown";
	}

	function formatError(value: unknown): string {
		if (!value) return "";
		if (typeof value === "string") return value;
		if (Array.isArray(value)) return value.map(formatError).filter(Boolean).join("\n");
		const typed = value as DesktopError & { message?: string; code?: string; name?: string; cause?: unknown };
		if (typed.cause && typed.cause !== value) {
			const causeMessage = formatError(typed.cause);
			if (causeMessage) return causeMessage;
		}
		if (typed.code && typed.message) return `${typed.code}: ${typed.message}`;
		if (typed.message) return typed.message;
		try {
			return JSON.stringify(value);
		} catch {
			return String(value);
		}
	}

	function showError(value: unknown) {
		const message = formatError(value);
		if (!message) return;
		error = message;
		notice = "";
		if (lastToast !== message) {
			lastToast = message;
			toast.error(message);
		}
	}

	function showNotice(message: string) {
		notice = message;
		error = "";
		if (lastToast !== message) {
			lastToast = message;
			toast.success(message);
		}
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

	function formatUnixMS(value: number | undefined): string {
		if (!value) return "-";
		return new Date(value).toLocaleString();
	}

	function activeTabLabel(): string {
		return tabs.find((tab) => tab.id === activeTab)?.label ?? "Status";
	}

	function statusBadgeVariant(level: string | undefined): "default" | "secondary" | "destructive" | "outline" {
		if (level === "ERROR" || level === "failed") return "destructive";
		if (level === "WARN" || level === "pending") return "secondary";
		return "outline";
	}

	function isBusy(name: string): boolean {
		return actionBusy === name;
	}
</script>

<Toaster richColors position="top-right" />

<div class="min-h-screen bg-muted/40 text-foreground">
	<div class="grid min-h-screen grid-cols-[240px_minmax(0,1fr)]">
		<aside class="border-r bg-background px-3 py-4">
			<div class="flex items-center gap-3 px-2">
				<div class="grid size-9 place-items-center rounded-lg bg-primary text-sm font-semibold text-primary-foreground">
					P
				</div>
				<div class="min-w-0">
					<div class="truncate text-sm font-semibold">ProfileDeck</div>
					<div class="truncate text-xs text-muted-foreground">{dashboard?.info.Version ?? "dev"}</div>
				</div>
			</div>

			<Separator class="my-4" />

			<Tabs.Root bind:value={activeTab} orientation="vertical" class="min-h-[calc(100vh-9rem)]">
				<Tabs.List variant="line" class="flex w-full flex-col items-stretch gap-1 bg-transparent p-0">
					{#each tabs as tab}
						<Tabs.Trigger value={tab.id} class="justify-start">
							<svelte:component this={tab.icon} data-icon="inline-start" />
							{tab.label}
						</Tabs.Trigger>
					{/each}
				</Tabs.List>
			</Tabs.Root>

			<div class="mt-4 rounded-lg border bg-card p-3">
				<div class="text-xs text-muted-foreground">Runtime</div>
				<div class="mt-1 flex items-center gap-2">
					<Badge variant={dashboard?.status.initialized ? "default" : "secondary"}>
						{dashboard?.status.initialized ? "Ready" : "Not initialized"}
					</Badge>
				</div>
			</div>
		</aside>

		<main class="min-w-0 p-5">
			<header class="mb-4 flex items-start justify-between gap-4">
				<div class="min-w-0">
					<h1 class="text-2xl font-semibold">{activeTabLabel()}</h1>
					<p class="mt-1 truncate text-sm text-muted-foreground">
						{dashboard?.status.runtime_root || dashboard?.environment.config_dir || "ProfileDeck runtime"}
					</p>
				</div>
				<div class="flex items-center gap-2">
					{#if actionBusy || loading}
						<Button variant="outline" onclick={cancelCurrentAction}>
							<LoaderIcon data-icon="inline-start" class="animate-spin" />
							Cancel
						</Button>
					{/if}
					<Button variant="outline" onclick={initialize} disabled={!!actionBusy}>
						Init
					</Button>
					<Button variant="outline" onclick={refreshAll} disabled={loading || !!actionBusy}>
						{#if loading}
							<Spinner data-icon="inline-start" />
							Refreshing
						{:else}
							<RefreshCcwIcon data-icon="inline-start" />
							Refresh
						{/if}
					</Button>
				</div>
			</header>

			<div class="flex flex-col gap-3">
				{#if error}
					<Alert.Root variant="destructive">
						<AlertTriangleIcon />
						<Alert.Title>Operation failed</Alert.Title>
						<Alert.Description>{error}</Alert.Description>
					</Alert.Root>
				{/if}
				{#if notice}
					<Alert.Root>
						<CheckIcon />
						<Alert.Title>Done</Alert.Title>
						<Alert.Description>{notice}</Alert.Description>
					</Alert.Root>
				{/if}
				{#if dashboard?.startup_error}
					<Alert.Root variant="destructive">
						<AlertTriangleIcon />
						<Alert.Title>{dashboard.startup_error.code}</Alert.Title>
						<Alert.Description>{dashboard.startup_error.message}</Alert.Description>
					</Alert.Root>
				{/if}
				{#if missingActiveCodexProfileID()}
					<Alert.Root>
						<AlertTriangleIcon />
						<Alert.Title>Active Codex profile is missing</Alert.Title>
						<Alert.Description>
							The active state points to {missingActiveCodexProfileID()}, but that profile no longer exists.
						</Alert.Description>
					</Alert.Root>
				{/if}
			</div>

			<div class="mt-4">
				{#if activeTab === "dashboard"}
					<section class="grid grid-cols-4 gap-3">
						<Card.Root>
							<Card.Header>
								<Card.Description>Database</Card.Description>
								<Card.Title>{dashboard?.status.schema_healthy ? "Healthy" : "Unavailable"}</Card.Title>
							</Card.Header>
						</Card.Root>
						<Card.Root>
							<Card.Header>
								<Card.Description>Doctor</Card.Description>
								<Card.Title>{dashboard?.doctor?.overall_level ?? "unknown"}</Card.Title>
							</Card.Header>
						</Card.Root>
						<Card.Root>
							<Card.Header>
								<Card.Description>Codex Profile</Card.Description>
								<Card.Title>{activeCodexProfileLabel()}</Card.Title>
							</Card.Header>
						</Card.Root>
						<Card.Root>
							<Card.Header>
								<Card.Description>Usage</Card.Description>
								<Card.Title>{costLabel(dashboard?.usage ?? null)}</Card.Title>
							</Card.Header>
						</Card.Root>
					</section>

					<Card.Root class="mt-4">
						<Card.Header>
							<Card.Title>Operations</Card.Title>
							<Card.Description>Pending and failed operation records from doctor.</Card.Description>
							<Card.Action>
								<Button variant="outline" onclick={runDoctor} disabled={!!actionBusy}>
									Run Doctor
								</Button>
							</Card.Action>
						</Card.Header>
						<Card.Content>
							<OperationTable operations={dashboard?.doctor?.operations ?? []} {actionBusy} onRecover={recoverFailedSwitch} />
						</Card.Content>
					</Card.Root>
				{/if}

				{#if activeTab === "codex"}
					<div class="grid gap-4">
						<Card.Root>
							<Card.Header>
								<Card.Title>Detection</Card.Title>
								<Card.Description>Codex paths and compatibility checks.</Card.Description>
								<Card.Action>
									<Button variant="outline" onclick={detectCodex} disabled={!!actionBusy}>
										{#if isBusy("detect")}<Spinner data-icon="inline-start" />{/if}
										Detect
									</Button>
								</Card.Action>
							</Card.Header>
							<Card.Content>
								{#if detectResult}
									<dl class="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
										<dt class="text-muted-foreground">Codex dir</dt><dd class="truncate">{detectResult.codex_dir}</dd>
										<dt class="text-muted-foreground">Config</dt><dd>{detectResult.config_status}</dd>
										<dt class="text-muted-foreground">Auth</dt><dd>{detectResult.auth_status}</dd>
										<dt class="text-muted-foreground">Provider</dt>
										<dd>{detectResult.provider_compatible ? "compatible" : "needs review"}</dd>
									</dl>
									<div class="mt-3 flex flex-col gap-2">
										{#each detectResult.warnings ?? [] as warning}
											<Alert.Root><AlertTriangleIcon /><Alert.Description>{warning}</Alert.Description></Alert.Root>
										{/each}
									</div>
								{:else}
									<EmptyState title="No detection result" description="Run detection to inspect local Codex configuration." />
								{/if}
							</Card.Content>
						</Card.Root>

						<Card.Root>
							<Card.Header>
								<Card.Title>Profile</Card.Title>
								<Card.Description>Capture current Codex state or create a managed Codex profile.</Card.Description>
								<Card.Action class="flex gap-2">
									<Button variant="outline" onclick={captureCodexProfile} disabled={!!actionBusy}>
										{#if isBusy("capture")}<Spinner data-icon="inline-start" />{/if}
										Capture
									</Button>
									<Button onclick={setManagedCodexProfile} disabled={!!actionBusy}>
										{#if isBusy("managed")}<Spinner data-icon="inline-start" />{/if}
										Save Managed
									</Button>
								</Card.Action>
							</Card.Header>
							<Card.Content>
								<Field.Group class="grid grid-cols-2 gap-3">
									<Field.Field>
										<Field.Label for="profile-id">Profile ID</Field.Label>
										<Input id="profile-id" bind:value={codexProfileID} placeholder="work" />
									</Field.Field>
									<Field.Field>
										<Field.Label for="account-id">Account ID</Field.Label>
										<Input id="account-id" bind:value={codexAccountID} placeholder="work" />
									</Field.Field>
									<Field.Field>
										<Field.Label for="profile-name">Name</Field.Label>
										<Input id="profile-name" bind:value={codexProfileName} placeholder="Work" />
									</Field.Field>
									<Field.Field>
										<Field.Label for="profile-description">Description</Field.Label>
										<Input id="profile-description" bind:value={codexProfileDescription} />
									</Field.Field>
									<Field.Field>
										<Field.Label for="model">Model</Field.Label>
										<Input id="model" bind:value={managedModel} />
									</Field.Field>
									<Field.Field>
										<Field.Label for="model-provider">Model provider</Field.Label>
										<Input id="model-provider" bind:value={managedModelProvider} />
									</Field.Field>
									<Field.Field class="col-span-2">
										<Field.Label for="base-url">Base URL</Field.Label>
										<Input id="base-url" bind:value={managedBaseURL} placeholder="optional" />
									</Field.Field>
								</Field.Group>
							</Card.Content>
						</Card.Root>

						<Card.Root>
							<Card.Header>
								<Card.Title>Accounts</Card.Title>
								<Card.Action>
									<Button variant="outline" onclick={refreshAccounts}>Refresh</Button>
								</Card.Action>
							</Card.Header>
							<Card.Content>
								{#if accounts.length}
									<Table.Root>
										<Table.Header>
											<Table.Row>
												<Table.Head>Account</Table.Head>
												<Table.Head>Name</Table.Head>
												<Table.Head>Kind</Table.Head>
												<Table.Head>SHA256</Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#each accounts as account}
												<Table.Row>
													<Table.Cell>{account.account_id}</Table.Cell>
													<Table.Cell>{account.display_name}</Table.Cell>
													<Table.Cell>{account.secret_kind}</Table.Cell>
													<Table.Cell>{account.payload_sha256.slice(0, 12)}</Table.Cell>
												</Table.Row>
											{/each}
										</Table.Body>
									</Table.Root>
								{:else}
									<EmptyState title="No accounts" description="Import or capture a Codex account first." />
								{/if}
							</Card.Content>
						</Card.Root>
					</div>
				{/if}

				{#if activeTab === "profiles"}
					<div class="grid gap-4">
						<Card.Root>
							<Card.Header>
								<Card.Title>Profiles</Card.Title>
								<Card.Description>Codex-capable ProfileDeck profiles.</Card.Description>
								<Card.Action>
									<Button variant="outline" onclick={refreshAll}>Refresh</Button>
								</Card.Action>
							</Card.Header>
							<Card.Content>
								{#if profiles().length}
									<Table.Root>
										<Table.Header>
											<Table.Row>
												<Table.Head>ID</Table.Head>
												<Table.Head>Name</Table.Head>
												<Table.Head>Description</Table.Head>
												<Table.Head>Status</Table.Head>
												<Table.Head></Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#each profiles() as profile}
												<Table.Row>
													<Table.Cell>{profile.id}</Table.Cell>
													<Table.Cell>{profile.name}</Table.Cell>
													<Table.Cell>{profile.description || "-"}</Table.Cell>
													<Table.Cell>
														{#if profile.id === activeCodexProfileID()}
															<Badge>Active</Badge>
														{:else}
															<Badge variant="outline">Idle</Badge>
														{/if}
													</Table.Cell>
													<Table.Cell class="text-right">
														<div class="flex justify-end gap-2">
															<Button variant="outline" size="sm" onclick={() => loadTargets(profile.id)}>Targets</Button>
															<Button size="sm" onclick={() => openSwitch(profile.id)}>Switch</Button>
														</div>
													</Table.Cell>
												</Table.Row>
											{/each}
										</Table.Body>
									</Table.Root>
								{:else}
									<EmptyState title="No profiles" description="Create or capture a Codex profile first." />
								{/if}
							</Card.Content>
						</Card.Root>

						<Card.Root>
							<Card.Header>
								<Card.Title>{selectedProfile()?.name ?? "Targets"}</Card.Title>
							</Card.Header>
							<Card.Content>
								{#if targets.length}
									<Table.Root>
										<Table.Header>
											<Table.Row>
												<Table.Head>Target</Table.Head>
												<Table.Head>Path</Table.Head>
												<Table.Head>Strategy</Table.Head>
												<Table.Head>Preview</Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#each targets as target}
												<Table.Row>
													<Table.Cell>{target.target_id}</Table.Cell>
													<Table.Cell class="max-w-xs truncate">{target.path}</Table.Cell>
													<Table.Cell>{target.format} / {target.strategy}</Table.Cell>
													<Table.Cell><pre class="max-h-28 overflow-auto rounded-lg bg-muted p-2 text-xs">{target.value_preview.content}</pre></Table.Cell>
												</Table.Row>
											{/each}
										</Table.Body>
									</Table.Root>
								{:else}
									<EmptyState title="No target selected" description="Choose a profile to inspect its target config." />
								{/if}
							</Card.Content>
						</Card.Root>
					</div>
				{/if}

				{#if activeTab === "switch"}
					<Card.Root>
						<Card.Header>
							<Card.Title>Plan</Card.Title>
							<Card.Description>Preview the redacted target diff before applying a profile.</Card.Description>
							<Card.Action class="flex min-w-[420px] gap-2">
								<Select.Root type="single" bind:value={selectedProfileID}>
									<Select.Trigger class="w-56">{selectedProfileLabel()}</Select.Trigger>
									<Select.Content>
										<Select.Group>
											{#each profiles() as profile}
												<Select.Item value={profile.id}>{profile.name || profile.id}</Select.Item>
											{/each}
										</Select.Group>
									</Select.Content>
								</Select.Root>
								<Button variant="outline" onclick={buildSwitchPlan} disabled={!selectedProfileID || !!actionBusy}>
									{#if isBusy("plan")}<Spinner data-icon="inline-start" />{/if}
									Preview
								</Button>
								<Button onclick={applySwitch} disabled={!canApplyPlan(switchPlan) || !!actionBusy}>
									{#if isBusy("apply-switch")}<Spinner data-icon="inline-start" />{/if}
									Apply
								</Button>
							</Card.Action>
						</Card.Header>
						<Card.Content>
							{#if switchPlan}
								{@const counts = planCounts(switchPlan)}
								<div class="mb-3 grid grid-cols-4 gap-3">
									<MetricCard label="Create" value={counts.create} />
									<MetricCard label="Update" value={counts.update} />
									<MetricCard label="Noop" value={counts.noop} />
									<MetricCard label="Unsupported" value={counts.unsupported} />
								</div>
								<div class="mb-3 rounded-lg border bg-muted/40 p-3 text-xs">
									<span class="text-muted-foreground">Fingerprint</span>
									<div class="mt-1 break-all font-mono">{switchPlan.plan_fingerprint}</div>
								</div>
								<div class="mb-3 flex flex-col gap-2">
									{#each switchPlan.warnings ?? [] as warning}
										<Alert.Root><AlertTriangleIcon /><Alert.Description>{warning}</Alert.Description></Alert.Root>
									{/each}
								</div>
								<div class="grid gap-3">
									{#each switchPlan.operations ?? [] as op}
										<Card.Root>
											<Card.Header>
												<Card.Title class="text-base">{op.target_id}</Card.Title>
												<Card.Description class="truncate">{op.path}</Card.Description>
												<Card.Action>
													<Badge variant={op.action === "unsupported" ? "destructive" : "secondary"}>{op.action}</Badge>
												</Card.Action>
											</Card.Header>
											<Card.Content>
												<pre class="max-h-72 overflow-auto rounded-lg bg-muted p-3 text-xs leading-relaxed">{operationPreview(op)}</pre>
											</Card.Content>
										</Card.Root>
									{/each}
								</div>
							{:else}
								<EmptyState title="No plan" description="Select a profile and preview the switch plan first." />
							{/if}
						</Card.Content>
					</Card.Root>
				{/if}

				{#if activeTab === "recovery"}
					<div class="grid gap-4">
						<Card.Root>
							<Card.Header>
								<Card.Title>Backups</Card.Title>
								<Card.Action><Button variant="outline" onclick={refreshBackups}>Refresh</Button></Card.Action>
							</Card.Header>
							<Card.Content>
								{#if backups.length}
									<Table.Root>
										<Table.Header>
											<Table.Row>
												<Table.Head>ID</Table.Head>
												<Table.Head>Profile</Table.Head>
												<Table.Head>Status</Table.Head>
												<Table.Head>Created</Table.Head>
												<Table.Head></Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#each backups as backup}
												<Table.Row>
													<Table.Cell class="font-mono text-xs">{backup.backup_id}</Table.Cell>
													<Table.Cell>{backup.profile_id}</Table.Cell>
													<Table.Cell>
														<Badge variant={backup.valid ? "secondary" : "destructive"}>
															{backup.valid ? backup.operation_status : backup.invalid_reason}
														</Badge>
													</Table.Cell>
													<Table.Cell>{formatUnixMS(backup.created_at_unix_ms)}</Table.Cell>
													<Table.Cell class="text-right">
														<div class="flex justify-end gap-2">
															<Button variant="outline" size="sm" onclick={() => showBackup(backup.backup_id)}>Show</Button>
															<ConfirmAction
																label="Rollback"
																title="Apply rollback?"
																description="This restores target files through the transaction safety layer."
																disabled={!backup.rollback_supported}
																onConfirm={() => applyRollback(backup.backup_id)}
															/>
														</div>
													</Table.Cell>
												</Table.Row>
											{/each}
										</Table.Body>
									</Table.Root>
								{:else}
									<EmptyState title="No backups" description="Backups are created by switch and rollback operations." />
								{/if}
							</Card.Content>
						</Card.Root>

						{#if selectedBackup}
							<Card.Root>
								<Card.Header>
									<Card.Title>{selectedBackup.backup_id}</Card.Title>
									<Card.Description>{selectedBackup.path}</Card.Description>
								</Card.Header>
								<Card.Content>
									<Table.Root>
										<Table.Header>
											<Table.Row>
												<Table.Head>Target</Table.Head>
												<Table.Head>Action</Table.Head>
												<Table.Head>Path</Table.Head>
												<Table.Head>SHA256</Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#each selectedBackup.entries ?? [] as entry}
												<Table.Row>
													<Table.Cell>{entry.target_id}</Table.Cell>
													<Table.Cell>{entry.action}</Table.Cell>
													<Table.Cell class="max-w-xs truncate">{entry.path}</Table.Cell>
													<Table.Cell>{entry.before_sha256.slice(0, 12)}</Table.Cell>
												</Table.Row>
											{/each}
										</Table.Body>
									</Table.Root>
								</Card.Content>
							</Card.Root>
						{/if}
					</div>
				{/if}

				{#if activeTab === "usage"}
					<Card.Root>
						<Card.Header>
							<Card.Title>Codex Usage</Card.Title>
							<Card.Description>Local usage and estimated cost from parseable session logs.</Card.Description>
							<Card.Action>
								<Button onclick={syncUsage} disabled={!!actionBusy}>
									{#if isBusy("usage-sync")}<Spinner data-icon="inline-start" />{/if}
									Sync
								</Button>
							</Card.Action>
						</Card.Header>
						<Card.Content>
							<section class="grid grid-cols-4 gap-3">
								<MetricCard label="Events" value={usageSummary?.event_count ?? 0} />
								<MetricCard label="Input" value={usageSummary?.input_tokens ?? 0} />
								<MetricCard label="Output" value={usageSummary?.output_tokens ?? 0} />
								<MetricCard label="Cost" value={costLabel(usageSummary)} />
							</section>
							{#if usageSyncResult}
								<div class="mt-4 grid grid-cols-4 gap-3">
									<MetricCard label="Scanned" value={usageSyncResult.scanned_files} />
									<MetricCard label="Imported" value={usageSyncResult.imported_events} />
									<MetricCard label="Duplicates" value={usageSyncResult.skipped_duplicate_events} />
									<MetricCard label="Invalid" value={usageSyncResult.invalid_lines} />
								</div>
							{/if}
						</Card.Content>
					</Card.Root>
				{/if}

				{#if activeTab === "doctor"}
					<Card.Root>
						<Card.Header>
							<Card.Title>Doctor</Card.Title>
							<Card.Action class="flex gap-2">
								<Button variant="outline" onclick={runDoctor} disabled={!!actionBusy}>
									{#if isBusy("doctor")}<Spinner data-icon="inline-start" />{/if}
									Run
								</Button>
								<ConfirmAction
									label="Repair Lock"
									title="Repair stale lock?"
									description="Only safe stale-lock repairs are allowed."
									disabled={!doctorResult?.lock.repairable || !!actionBusy}
									onConfirm={repairLock}
								/>
							</Card.Action>
						</Card.Header>
						<Card.Content>
							{#if doctorResult}
								<section class="mb-4 grid grid-cols-4 gap-3">
									<MetricCard label="Overall" value={doctorResult.overall_level} />
									<MetricCard label="Lock" value={doctorResult.lock.level} />
									<MetricCard label="Pending" value={dashboard?.status.pending_operations ?? 0} />
									<MetricCard label="Failed" value={dashboard?.status.failed_operations ?? 0} />
								</section>
								{#if doctorResult.findings?.length}
									<Table.Root>
										<Table.Header>
											<Table.Row>
												<Table.Head>Finding</Table.Head>
												<Table.Head>Level</Table.Head>
												<Table.Head>Message</Table.Head>
											</Table.Row>
										</Table.Header>
										<Table.Body>
											{#each doctorResult.findings ?? [] as finding}
												<Table.Row>
													<Table.Cell>{finding.id}</Table.Cell>
													<Table.Cell><Badge variant={statusBadgeVariant(finding.level)}>{finding.level}</Badge></Table.Cell>
													<Table.Cell>{finding.message}</Table.Cell>
												</Table.Row>
											{/each}
										</Table.Body>
									</Table.Root>
								{/if}
								<div class="mt-4">
									<OperationTable operations={doctorResult.operations ?? []} {actionBusy} onRecover={recoverFailedSwitch} />
								</div>
							{:else}
								<EmptyState title="No doctor result" description="Run doctor to inspect runtime health." />
							{/if}
						</Card.Content>
					</Card.Root>
				{/if}
			</div>
		</main>
	</div>
</div>
