<script lang="ts">
	import { _ } from "svelte-i18n";
	import GaugeIcon from "@lucide/svelte/icons/gauge";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import IconAction from "$lib/components/app/IconAction.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import { Badge } from "$lib/components/ui/badge";
	import * as Card from "$lib/components/ui/card";
	import * as Empty from "$lib/components/ui/empty";
	import { Progress } from "$lib/components/ui/progress";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { currentDesktopLocale } from "$lib/i18n";
	import { cn } from "$lib/utils";

	import type { GrokBuildProfileQuota } from "../../../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
	import ProfileQuotaFreshness from "./ProfileQuotaFreshness.svelte";
	import type { GrokBuildQuotaCheckOutcome } from "./grok-build-quota.svelte.js";

	let {
		quota,
		loading,
		active,
		checkedAtUnixMS,
		checkOutcome,
		nowUnixMS,
		disabled,
		onRefresh,
	}: {
		quota: GrokBuildProfileQuota | null;
		loading: boolean;
		active: boolean;
		checkedAtUnixMS: number;
		checkOutcome: GrokBuildQuotaCheckOutcome;
		nowUnixMS: number;
		disabled: boolean;
		onRefresh: () => void;
	} = $props();

	function present(value: number | null | undefined): value is number {
		return value !== null && value !== undefined;
	}

	function formatPercent(value: number): string {
		return Number.isInteger(value) ? String(value) : value.toFixed(1);
	}

	function formatMoney(cents: number): string {
		return new Intl.NumberFormat(currentDesktopLocale(), { style: "currency", currency: "USD" }).format(cents / 100);
	}

	function formatTimestamp(unixSeconds: number): string {
		return new Date(unixSeconds * 1000).toLocaleString(currentDesktopLocale(), {
			month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
		});
	}

	function formatFetchedAt(unixMS: number): string {
		return new Date(unixMS).toLocaleString(currentDesktopLocale(), {
			month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
		});
	}

	function formatWindow(type: string | undefined, seconds: number | null | undefined): string {
		const normalized = type?.toUpperCase() ?? "";
		if (normalized.includes("WEEKLY")) return $_("grokBuild.quota.windowWeekly");
		if (normalized.includes("MONTHLY")) return $_("grokBuild.quota.windowMonthly");
		if (present(seconds) && seconds > 0 && seconds % 604800 === 0) return $_("quota.windowWeeks", { values: { count: seconds / 604800 } });
		if (present(seconds) && seconds > 0 && seconds % 86400 === 0) return $_("quota.windowDays", { values: { count: seconds / 86400 } });
		if (present(seconds) && seconds > 0 && seconds % 3600 === 0) return $_("quota.windowHours", { values: { count: seconds / 3600 } });
		return $_("grokBuild.quota.currentPeriod");
	}

	function hasMoney(value: GrokBuildProfileQuota): boolean {
		const snapshot = value.snapshot;
		return !!snapshot && (
			present(snapshot.included_limit_cents)
			|| present(snapshot.prepaid_balance_cents)
			|| present(snapshot.on_demand_cap_cents)
			|| present(snapshot.on_demand_used_cents)
			|| snapshot.on_demand_enabled !== null && snapshot.on_demand_enabled !== undefined
		);
	}

	function hasDisplayableData(value: GrokBuildProfileQuota): boolean {
		return !!value.snapshot && (
			present(value.snapshot.remaining_percent)
			|| !!value.snapshot.subscription_tier
			|| hasMoney(value)
		);
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>{$_("grokBuild.quota.title")}</Card.Title>
		<Card.Description>{$_("grokBuild.quota.description")}</Card.Description>
		<Card.Action>
			<div class="flex items-center gap-2">
				<ProfileQuotaFreshness {checkedAtUnixMS} {checkOutcome} {nowUnixMS} />
				{#if active}
					<IconAction label={$_("grokBuild.quota.refresh")} disabled={disabled || loading} onclick={onRefresh}>
						{#if loading}<Spinner />{:else}<RefreshCwIcon />{/if}
					</IconAction>
				{/if}
			</div>
		</Card.Action>
	</Card.Header>
	<Card.Content class="flex flex-col gap-4">
		<Alert.Root>
			<GaugeIcon data-icon="inline-start" />
			<Alert.Title>{$_("grokBuild.quota.sessionOnlyTitle")}</Alert.Title>
			<Alert.Description>{$_("grokBuild.quota.sessionOnlyDescription")}</Alert.Description>
		</Alert.Root>
		{#if loading && !quota?.snapshot}
			<div class="flex flex-col gap-4">
				<Skeleton class="h-5 w-32" />
				<Skeleton class="h-28 w-full" />
			</div>
		{:else if !quota}
			<Empty.Root class="border-0 py-6">
				<Empty.Header>
					<Empty.Media variant="icon"><GaugeIcon /></Empty.Media>
					<Empty.Title>{active ? $_("grokBuild.quota.notLoadedTitle") : $_("grokBuild.quota.noSnapshotTitle")}</Empty.Title>
					<Empty.Description>{active ? $_("grokBuild.quota.notLoadedDescription") : $_("grokBuild.quota.noSnapshotDescription")}</Empty.Description>
				</Empty.Header>
			</Empty.Root>
		{:else if !quota.snapshot}
			<Alert.Root variant={quota.status === "auth_required" ? "destructive" : "default"}>
				<TriangleAlertIcon data-icon="inline-start" />
				<Alert.Title>
					{quota.status === "auth_required" ? $_("grokBuild.quota.authRequired") : quota.status === "unsupported" ? $_("grokBuild.quota.unsupported") : quota.status === "inactive" ? $_("grokBuild.quota.inactive") : $_("grokBuild.quota.unavailable")}
				</Alert.Title>
				<Alert.Description>
					{quota.status === "auth_required" ? $_("grokBuild.quota.authRequiredDescription") : quota.status === "unsupported" ? $_("grokBuild.quota.unsupportedDescription") : quota.status === "inactive" ? $_("grokBuild.quota.inactiveDescription") : $_("grokBuild.quota.unavailableDescription")}
				</Alert.Description>
			</Alert.Root>
		{:else}
			<div class="flex flex-col gap-4">
				{#if quota.status === "inactive" || !active}
					<Alert.Root>
						<GaugeIcon data-icon="inline-start" />
						<Alert.Title>{$_("grokBuild.quota.sessionSnapshotTitle")}</Alert.Title>
						<Alert.Description>{$_("grokBuild.quota.sessionSnapshotDescription")}</Alert.Description>
					</Alert.Root>
				{:else if quota.status !== "available"}
					<Alert.Root variant={quota.status === "auth_required" ? "destructive" : "default"}>
						<TriangleAlertIcon data-icon="inline-start" />
						<Alert.Title>{quota.status === "auth_required" ? $_("grokBuild.quota.authRequired") : quota.status === "unsupported" ? $_("grokBuild.quota.unsupported") : $_("grokBuild.quota.unavailable")}</Alert.Title>
						<Alert.Description>{quota.status === "auth_required" ? $_("grokBuild.quota.staleAuthDescription") : quota.status === "unsupported" ? $_("grokBuild.quota.staleUnsupportedDescription") : $_("grokBuild.quota.staleUnavailableDescription")}</Alert.Description>
					</Alert.Root>
				{/if}

				<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
					{#if quota.snapshot.subscription_tier}<Badge variant="outline">{quota.snapshot.subscription_tier}</Badge>{/if}
					<span>{$_("grokBuild.quota.fetchedAt", { values: { value: formatFetchedAt(quota.snapshot.fetched_at_unix_ms) } })}</span>
				</div>

				{#if present(quota.snapshot.remaining_percent)}
					<section class="flex flex-col gap-3 rounded-lg border p-4">
						<div class="flex items-center justify-between gap-3">
							<span class="text-sm font-medium">{formatWindow(quota.snapshot.period_type, quota.snapshot.period_duration_seconds)}</span>
							<span class={cn("text-sm font-medium", quota.snapshot.remaining_percent <= 0 && "text-destructive")}>
								{$_("grokBuild.quota.remaining", { values: { value: formatPercent(quota.snapshot.remaining_percent) } })}
							</span>
						</div>
						<Progress value={Math.min(100, Math.max(0, quota.snapshot.remaining_percent))} aria-label={$_("grokBuild.quota.remaining", { values: { value: formatPercent(quota.snapshot.remaining_percent) } })} />
						{#if present(quota.snapshot.reset_at_unix_seconds)}
							<span class="text-xs text-muted-foreground">{$_("grokBuild.quota.resetsAt", { values: { value: formatTimestamp(quota.snapshot.reset_at_unix_seconds) } })}</span>
						{/if}
					</section>
				{/if}

				{#if hasMoney(quota)}
					<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
						{#if present(quota.snapshot.included_limit_cents)}
							<div class="flex flex-col gap-1 rounded-lg border p-3">
								<span class="text-xs text-muted-foreground">{$_("grokBuild.quota.includedCredits")}</span>
								<span class="text-sm font-medium">{present(quota.snapshot.included_used_cents) ? `${formatMoney(quota.snapshot.included_used_cents)} / ` : ""}{formatMoney(quota.snapshot.included_limit_cents)}</span>
							</div>
						{/if}
						{#if present(quota.snapshot.prepaid_balance_cents)}
							<div class="flex flex-col gap-1 rounded-lg border p-3">
								<span class="text-xs text-muted-foreground">{$_("grokBuild.quota.prepaid")}</span>
								<span class="text-sm font-medium">{formatMoney(quota.snapshot.prepaid_balance_cents)}</span>
							</div>
						{/if}
						{#if present(quota.snapshot.on_demand_cap_cents) || present(quota.snapshot.on_demand_used_cents) || quota.snapshot.on_demand_enabled !== null && quota.snapshot.on_demand_enabled !== undefined}
							<div class="flex flex-col gap-1 rounded-lg border p-3">
								<span class="text-xs text-muted-foreground">{$_("grokBuild.quota.onDemand")}</span>
								{#if quota.snapshot.on_demand_enabled === false}
									<span class="text-sm font-medium">{$_("grokBuild.quota.onDemandOff")}</span>
								{:else}
									<span class="text-sm font-medium">
										{present(quota.snapshot.on_demand_used_cents) ? formatMoney(quota.snapshot.on_demand_used_cents) : "—"}
										{#if present(quota.snapshot.on_demand_cap_cents)} / {formatMoney(quota.snapshot.on_demand_cap_cents)}{/if}
									</span>
								{/if}
							</div>
						{/if}
					</div>
				{:else if !hasDisplayableData(quota)}
					<Empty.Root class="border-0 py-6">
						<Empty.Header>
							<Empty.Media variant="icon"><GaugeIcon /></Empty.Media>
							<Empty.Title>{$_("grokBuild.quota.noDisplayableTitle")}</Empty.Title>
							<Empty.Description>{$_("grokBuild.quota.noDisplayableDescription")}</Empty.Description>
						</Empty.Header>
					</Empty.Root>
				{/if}
			</div>
		{/if}
	</Card.Content>
</Card.Root>
