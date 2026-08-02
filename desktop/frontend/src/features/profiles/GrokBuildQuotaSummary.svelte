<script lang="ts">
	import { _ } from "svelte-i18n";

	import { Badge } from "$lib/components/ui/badge";
	import { Progress } from "$lib/components/ui/progress";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { currentDesktopLocale } from "$lib/i18n";
	import { cn } from "$lib/utils";

	import type { GrokBuildProfileQuota } from "../../../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";

	let {
		quota,
		loading,
		nowUnixMS,
	}: {
		quota: GrokBuildProfileQuota | null;
		loading: boolean;
		nowUnixMS: number;
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

	function formatWindow(type: string | undefined, seconds: number | null | undefined): string {
		const normalized = type?.toUpperCase() ?? "";
		if (normalized.includes("WEEKLY")) return $_("grokBuild.quota.windowWeekly");
		if (normalized.includes("MONTHLY")) return $_("grokBuild.quota.windowMonthly");
		if (present(seconds) && seconds > 0 && seconds % 604800 === 0) return $_("quota.windowWeeks", { values: { count: seconds / 604800 } });
		if (present(seconds) && seconds > 0 && seconds % 86400 === 0) return $_("quota.windowDays", { values: { count: seconds / 86400 } });
		if (present(seconds) && seconds > 0 && seconds % 3600 === 0) return $_("quota.windowHours", { values: { count: seconds / 3600 } });
		return $_("grokBuild.quota.currentPeriod");
	}

	function isToday(value: Date, now: Date): boolean {
		return value.getFullYear() === now.getFullYear()
			&& value.getMonth() === now.getMonth()
			&& value.getDate() === now.getDate();
	}

	function formatReset(unixSeconds: number): string {
		const date = new Date(unixSeconds * 1000);
		const now = new Date(nowUnixMS);
		const options: Intl.DateTimeFormatOptions = isToday(date, now)
			? { hour: "2-digit", minute: "2-digit" }
			: {
				...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" as const }),
				month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
			};
		return date.toLocaleString(currentDesktopLocale(), options);
	}

	function hasDisplayableData(value: GrokBuildProfileQuota): boolean {
		const snapshot = value.snapshot;
		return !!snapshot && (
			present(snapshot.remaining_percent)
			|| !!snapshot.subscription_tier
			|| present(snapshot.prepaid_balance_cents)
			|| present(snapshot.on_demand_cap_cents)
			|| present(snapshot.on_demand_used_cents)
			|| present(snapshot.included_limit_cents)
		);
	}
</script>

{#if loading && !quota?.snapshot}
	<Skeleton class="h-14 w-full" />
{:else if quota?.snapshot}
	<div class="flex min-w-0 flex-col gap-2">
		<div class="flex flex-wrap items-center gap-2">
			{#if quota.status === "inactive"}<Badge variant="secondary">{$_("grokBuild.quota.sessionSnapshot")}</Badge>{/if}
			{#if quota.status === "auth_required"}<Badge variant="destructive">{$_("grokBuild.quota.authRequired")}</Badge>{/if}
			{#if quota.status === "unsupported"}<Badge variant="secondary">{$_("grokBuild.quota.unsupported")}</Badge>{/if}
			{#if quota.status === "unavailable"}<Badge variant="secondary">{$_("grokBuild.quota.unavailable")}</Badge>{/if}
			{#if quota.snapshot.subscription_tier}<Badge variant="outline">{quota.snapshot.subscription_tier}</Badge>{/if}
		</div>
		{#if present(quota.snapshot.remaining_percent)}
			<div class="grid min-w-0 gap-1">
				<span class="flex min-w-0 items-center justify-between gap-2 text-xs">
					<span class="truncate text-muted-foreground">{formatWindow(quota.snapshot.period_type, quota.snapshot.period_duration_seconds)}</span>
					<span class={cn("shrink-0 font-medium", quota.snapshot.remaining_percent <= 0 && "text-destructive")}>
						{$_("grokBuild.quota.remaining", { values: { value: formatPercent(quota.snapshot.remaining_percent) } })}
					</span>
				</span>
				<Progress value={Math.min(100, Math.max(0, quota.snapshot.remaining_percent))} aria-label={$_("grokBuild.quota.remaining", { values: { value: formatPercent(quota.snapshot.remaining_percent) } })} />
				{#if present(quota.snapshot.reset_at_unix_seconds)}
					<span class="truncate text-xs text-muted-foreground">{$_("grokBuild.quota.resetsAt", { values: { value: formatReset(quota.snapshot.reset_at_unix_seconds) } })}</span>
				{/if}
			</div>
		{/if}
		{#if present(quota.snapshot.included_limit_cents) || present(quota.snapshot.prepaid_balance_cents) || present(quota.snapshot.on_demand_used_cents) || present(quota.snapshot.on_demand_cap_cents)}
			<div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
				{#if present(quota.snapshot.included_limit_cents)}<span>{$_("grokBuild.quota.includedCredits")}: {present(quota.snapshot.included_used_cents) ? `${formatMoney(quota.snapshot.included_used_cents)} / ` : ""}{formatMoney(quota.snapshot.included_limit_cents)}</span>{/if}
				{#if present(quota.snapshot.prepaid_balance_cents)}<span>{$_("grokBuild.quota.prepaid")}: {formatMoney(quota.snapshot.prepaid_balance_cents)}</span>{/if}
				{#if present(quota.snapshot.on_demand_used_cents)}<span>{$_("grokBuild.quota.onDemandUsed")}: {formatMoney(quota.snapshot.on_demand_used_cents)}</span>{/if}
				{#if present(quota.snapshot.on_demand_cap_cents)}<span>{$_("grokBuild.quota.onDemandCap")}: {formatMoney(quota.snapshot.on_demand_cap_cents)}</span>{/if}
			</div>
		{:else if !hasDisplayableData(quota)}
			<div><Badge variant="secondary">{$_("grokBuild.quota.noDisplayableTitle")}</Badge></div>
		{/if}
	</div>
{:else if quota?.status === "auth_required"}
	<div><Badge variant="destructive">{$_("grokBuild.quota.authRequired")}</Badge></div>
{:else if quota?.status === "unsupported"}
	<div><Badge variant="secondary">{$_("grokBuild.quota.unsupported")}</Badge></div>
{:else if quota?.status === "unavailable"}
	<div><Badge variant="secondary">{$_("grokBuild.quota.unavailable")}</Badge></div>
{/if}
