<script lang="ts">
	import { onMount } from "svelte";
	import { Events, type CancellablePromise } from "@wailsio/runtime";
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import ClockIcon from "@lucide/svelte/icons/clock-3";
	import InfoIcon from "@lucide/svelte/icons/info";

	import { CodexService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexProfileQuotaRuntimeStatus,
		CodexQuotaRuntimeStatus,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend/models";
	import type {
		CodexProfileSettings,
		CodexSettings as CodexSettingsModel,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	import * as Alert from "$lib/components/ui/alert";
	import * as Card from "$lib/components/ui/card";
	import * as Empty from "$lib/components/ui/empty";
	import * as Field from "$lib/components/ui/field";
	import * as Select from "$lib/components/ui/select";
	import * as Switch from "$lib/components/ui/switch";
	import * as Tooltip from "$lib/components/ui/tooltip";
	import { Badge } from "$lib/components/ui/badge";
	import { Separator } from "$lib/components/ui/separator";
	import { Spinner } from "$lib/components/ui/spinner";
	import { isCancelError } from "$lib/desktop-errors";
	import { currentDesktopLocale, translate } from "$lib/i18n";

	interface Props {
		profilesRevision: number;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	}

	let { profilesRevision, showError, showNotice }: Props = $props();

	const quotaIntervals = [0, 300, 600, 1800, 3600] as const;
	const usageIntervals = [5, 15, 30, 60] as const;
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let settings = $state<CodexSettingsModel | null>(null);
	let runtime = $state<CodexQuotaRuntimeStatus>({ app_server_status: "unknown", profiles: [] });
	let loading = $state(true);
	let busyKeys = $state<string[]>([]);
	let observedProfilesRevision = $state<number | null>(null);
	let loadSequence = 0;

	let runtimeByProfileID = $derived.by(() => {
		const values: Record<string, CodexProfileQuotaRuntimeStatus> = {};
		for (const status of runtime.profiles ?? []) values[status.profile_id] = status;
		return values;
	});

	onMount(() => {
		void load();
		const off = Events.On("profiledeck:codex-quota-status", (event) => {
			const previousCompletion = latestRuntimeCompletion(runtime);
			const nextRuntime = (event.data as CodexQuotaRuntimeStatus) ?? { app_server_status: "unknown", profiles: [] };
			runtime = nextRuntime;
			if (latestRuntimeCompletion(nextRuntime) > previousCompletion) {
				void reloadProfileSettings(false);
			}
		});
		return () => {
			off();
			for (const promise of inFlight.values()) promise.cancel("unmount");
			inFlight.clear();
		};
	});

	$effect(() => {
		const revision = profilesRevision;
		if (observedProfilesRevision === null) {
			observedProfilesRevision = revision;
			return;
		}
		if (revision === observedProfilesRevision) return;
		observedProfilesRevision = revision;
		void reloadProfileSettings(true);
	});

	async function load() {
		const sequence = ++loadSequence;
		loading = true;
		try {
			const [nextSettings, nextRuntime] = await Promise.all([
				track("codex-settings", CodexService.GetSettings()),
				track("codex-quota-runtime", CodexService.QuotaRuntimeStatus()),
			]);
			settings = nextSettings;
			runtime = nextRuntime;
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			if (sequence === loadSequence) loading = false;
		}
	}

	async function reloadProfileSettings(showLoading: boolean) {
		const sequence = ++loadSequence;
		if (showLoading) loading = true;
		try {
			settings = await track("codex-settings", CodexService.GetSettings());
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			if (sequence === loadSequence) loading = false;
		}
	}

	function latestRuntimeCompletion(value: CodexQuotaRuntimeStatus): number {
		return Math.max(0, ...(value.profiles ?? []).map((profile) => profile.last_completed_at_unix_ms || 0));
	}

	async function changeUsageSyncInterval(value: string) {
		const interval = Number(value);
		if (!usageIntervals.includes(interval as (typeof usageIntervals)[number]) || settings?.usage_sync_interval_seconds === interval) return;
		await update("usage", {
			config_dir: "",
			usage_sync_interval_seconds: interval,
		});
	}

	async function changeQuotaInterval(profile: CodexProfileSettings, value: string) {
		const interval = Number(value);
		if (!quotaIntervals.includes(interval as (typeof quotaIntervals)[number]) || profile.quota_refresh_interval_seconds === interval) return;
		if (!profile.quota_supported && interval !== 0) return;
		await update(`quota:${profile.profile_id}`, {
			config_dir: "",
			profile_id: profile.profile_id,
			quota_refresh_interval_seconds: interval,
		});
	}

	async function changeKeepalive(profile: CodexProfileSettings, enabled: boolean) {
		if (profile.auth_keepalive_enabled === enabled) return;
		if (!profile.auth_keepalive_supported && enabled) return;
		await update(`keepalive:${profile.profile_id}`, {
			config_dir: "",
			profile_id: profile.profile_id,
			auth_keepalive_enabled: enabled,
		});
	}

	async function update(key: string, request: Parameters<typeof CodexService.UpdateSettings>[0]) {
		if (busyKeys.includes(key)) return;
		busyKeys = [...busyKeys, key];
		try {
			settings = await track(`codex-settings:${key}`, CodexService.UpdateSettings(request));
			runtime = await track("codex-quota-runtime", CodexService.QuotaRuntimeStatus());
			showNotice(translate("notice.settingsSaved.title"), translate("notice.settingsSaved.codexDescription"));
		} catch (error) {
			if (isCancelError(error)) return;
			showError(error);
			await load();
		} finally {
			busyKeys = busyKeys.filter((value) => value !== key);
		}
	}

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		inFlight.get(key)?.cancel("replaced");
		inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => {
			if (inFlight.get(key) === promise) inFlight.delete(key);
		}).catch(() => {});
		return promise;
	}

	function quotaIntervalLabel(seconds: number): string {
		if (seconds === 0) return translate("codexSettings.quota.off");
		return translate("codexSettings.quota.minutes", { count: seconds / 60 });
	}

	function formatTimestamp(value: number | undefined): string {
		if (!value) return translate("codexSettings.runtime.never");
		return new Date(value).toLocaleString(currentDesktopLocale(), { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
	}

	function runtimeLabel(status: CodexProfileQuotaRuntimeStatus | undefined): string {
		if (status?.running) return translate("codexSettings.runtime.running");
		if (!status || !status.last_completed_at_unix_ms) return translate("codexSettings.runtime.idle");
		if (status.error_code === "AUTH_PERMANENT") return translate("codexSettings.runtime.authPermanent");
		switch (status.status) {
			case "available": return status.last_task === "keepalive" ? translate("codexSettings.runtime.keptAlive") : translate("codexSettings.runtime.available");
			case "auth_required": return translate("codexSettings.runtime.authRequired");
			case "unsupported": return translate("codexSettings.runtime.unsupported");
			default: return translate("codexSettings.runtime.unavailable");
		}
	}

	function quotaHelp(profile: CodexProfileSettings): string {
		return profile.quota_supported ? translate("codexSettings.quota.description") : translate("codexSettings.quota.unsupported");
	}

	function keepaliveHelp(profile: CodexProfileSettings): string {
		if (!profile.auth_keepalive_supported) return translate("codexSettings.keepalive.unsupported");
		const description = translate("codexSettings.keepalive.description");
		if (profile.auth_keepalive_enabled && profile.quota_refresh_interval_seconds > 0) {
			return `${description} ${translate("codexSettings.keepalive.coveredByQuota")}`;
		}
		return description;
	}
</script>

{#snippet InfoTip(content: string)}
	<Tooltip.Root>
		<Tooltip.Trigger
			aria-label={$_("codexSettings.infoLabel")}
			class="inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
		>
			<InfoIcon class="size-3.5" />
		</Tooltip.Trigger>
		<Tooltip.Content side="top">{content}</Tooltip.Content>
	</Tooltip.Root>
{/snippet}

<Tooltip.Provider delayDuration={250}>
<div class="mx-auto flex w-full max-w-4xl flex-col gap-3">
	{#if runtime.app_server_status === "unavailable" || runtime.app_server_status === "incompatible"}
		<Alert.Root variant="destructive">
			<AlertTriangleIcon data-icon="inline-start" />
			<Alert.Title>{$_("codexSettings.appServer.title")}</Alert.Title>
			<Alert.Description>
				{runtime.app_server_status === "incompatible" ? $_("codexSettings.appServer.incompatible") : $_("codexSettings.appServer.unavailable")}
			</Alert.Description>
		</Alert.Root>
	{/if}

	<Card.Root>
		<Card.Header class="pb-3">
			<Card.Title>{$_("codexSettings.title")}</Card.Title>
		</Card.Header>
		<Card.Content>
			<Field.FieldGroup>
				<Field.Field orientation="horizontal">
					<Field.FieldContent>
						<div class="flex items-center gap-1">
							<Field.FieldLabel for="codex-usage-sync">{$_("codexSettings.usageSync.label")}</Field.FieldLabel>
							{@render InfoTip($_("codexSettings.usageSync.description"))}
						</div>
					</Field.FieldContent>
					<div class="flex min-w-40 items-center justify-end gap-2">
						{#if busyKeys.includes("usage")}<Spinner />{/if}
						<Select.Root type="single" value={String(settings?.usage_sync_interval_seconds ?? 15)} onValueChange={changeUsageSyncInterval}>
							<Select.Trigger id="codex-usage-sync" disabled={loading || busyKeys.includes("usage")}>
								{$_("codexSettings.usageSync.seconds", { values: { count: settings?.usage_sync_interval_seconds ?? 15 } })}
							</Select.Trigger>
							<Select.Content><Select.Group>
								{#each usageIntervals as seconds (seconds)}
									<Select.Item value={String(seconds)} label={$_("codexSettings.usageSync.seconds", { values: { count: seconds } })} />
								{/each}
							</Select.Group></Select.Content>
						</Select.Root>
					</div>
				</Field.Field>
			</Field.FieldGroup>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header class="pb-3">
			<Card.Title>{$_("codexSettings.profiles.title")}</Card.Title>
		</Card.Header>
		<Card.Content class="px-0">
			{#if loading}
				<div class="flex justify-center py-10"><Spinner /></div>
			{:else if !settings?.profiles?.length}
				<Empty.Root class="border-0 py-10">
					<Empty.Header><Empty.Media variant="icon"><ClockIcon /></Empty.Media><Empty.Title>{$_("codexSettings.profiles.emptyTitle")}</Empty.Title><Empty.Description>{$_("codexSettings.profiles.emptyDescription")}</Empty.Description></Empty.Header>
				</Empty.Root>
			{:else}
				{#each settings.profiles as profile, index (profile.profile_id)}
					{@const profileRuntime = runtimeByProfileID[profile.profile_id]}
					<div class="flex flex-col gap-3 px-4 py-4">
						<div class="flex flex-wrap items-start justify-between gap-3">
							<div class="min-w-0">
								<div class="flex flex-wrap items-center gap-2">
									<span class="font-medium">{profile.profile_name || profile.profile_id}</span>
									{#if profile.profile_name && profile.profile_name !== profile.profile_id}
										<span class="font-mono text-xs text-muted-foreground">{profile.profile_id}</span>
									{/if}
									<Badge variant="outline">{profile.auth_mode}</Badge>
									{#if profile.credential_reference_count > 1}
										<Badge variant="secondary">{$_("codexSettings.profiles.sharedCredentialBadge", { values: { count: profile.credential_reference_count } })}</Badge>
										{@render InfoTip($_("codexSettings.profiles.sharedCredential", { values: { count: profile.credential_reference_count } }))}
									{/if}
								</div>
							</div>
							<Badge variant={profileRuntime?.running ? "secondary" : "outline"}>{runtimeLabel(profileRuntime)}</Badge>
						</div>

						<div class="grid gap-x-6 gap-y-2 sm:grid-cols-2">
							<Field.Field orientation="horizontal" data-disabled={!profile.quota_supported || undefined}>
								<Field.FieldContent>
									<div class="flex items-center gap-1">
										<Field.FieldLabel for={`quota-interval-${profile.profile_id}`}>{$_("codexSettings.quota.label")}</Field.FieldLabel>
										{@render InfoTip(quotaHelp(profile))}
									</div>
								</Field.FieldContent>
								<div class="flex min-w-32 items-center justify-end gap-2">
									{#if busyKeys.includes(`quota:${profile.profile_id}`)}<Spinner />{/if}
									<Select.Root type="single" value={String(profile.quota_refresh_interval_seconds)} onValueChange={(value) => changeQuotaInterval(profile, value)}>
										<Select.Trigger id={`quota-interval-${profile.profile_id}`} disabled={loading || (!profile.quota_supported && profile.quota_refresh_interval_seconds === 0) || busyKeys.includes(`quota:${profile.profile_id}`)}>{quotaIntervalLabel(profile.quota_refresh_interval_seconds)}</Select.Trigger>
										<Select.Content><Select.Group>
											{#each quotaIntervals as seconds (seconds)}<Select.Item value={String(seconds)} label={quotaIntervalLabel(seconds)} />{/each}
										</Select.Group></Select.Content>
									</Select.Root>
								</div>
							</Field.Field>

							<Field.Field orientation="horizontal" data-disabled={!profile.auth_keepalive_supported || undefined}>
								<Field.FieldContent>
									<div class="flex items-center gap-1">
										<Field.FieldLabel for={`keepalive-${profile.profile_id}`}>{$_("codexSettings.keepalive.label")}</Field.FieldLabel>
										{@render InfoTip(keepaliveHelp(profile))}
									</div>
								</Field.FieldContent>
								<div class="flex items-center gap-2">
									{#if busyKeys.includes(`keepalive:${profile.profile_id}`)}<Spinner />{/if}
									<Switch.Root id={`keepalive-${profile.profile_id}`} checked={profile.auth_keepalive_enabled} disabled={loading || (!profile.auth_keepalive_supported && !profile.auth_keepalive_enabled) || busyKeys.includes(`keepalive:${profile.profile_id}`)} onCheckedChange={(value) => changeKeepalive(profile, value)} />
								</div>
							</Field.Field>
						</div>
						{#if !profile.auth_keepalive_supported}
							<Alert.Root class="py-2.5">
								<AlertTriangleIcon data-icon="inline-start" />
								<Alert.Title>{profile.quota_supported ? $_("codexSettings.keepalive.unsupportedTitle") : $_("codexSettings.quota.unsupported")}</Alert.Title>
							</Alert.Root>
						{/if}

						<div class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
							<span>{$_("codexSettings.runtime.lastRun")}: {formatTimestamp(profileRuntime?.last_completed_at_unix_ms)}</span>
							<span>{$_("codexSettings.runtime.nextRun")}: {formatTimestamp(profileRuntime?.next_run_at_unix_ms)}</span>
						</div>
					</div>
					{#if index < settings.profiles.length - 1}<Separator />{/if}
				{/each}
			{/if}
		</Card.Content>
	</Card.Root>
</div>
</Tooltip.Provider>
