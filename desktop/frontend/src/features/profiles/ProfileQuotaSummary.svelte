<script lang="ts">
	import { _ } from "svelte-i18n";

	import { Badge } from "$lib/components/ui/badge";
	import { Progress } from "$lib/components/ui/progress";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { currentDesktopLocale } from "$lib/i18n";
	import { cn } from "$lib/utils";

	import type {
		CodexProfileQuota,
		CodexQuotaWindow,
		CodexSub2APIQuotaSnapshot,
		CodexSub2APIQuotaWindow,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/codex/models";
	import { formatQuotaValue as formatQuotaAmount } from "./quota-format";

	let {
		quota,
		loading,
		nowUnixMS,
	}: {
		quota: CodexProfileQuota | null;
		loading: boolean;
		nowUnixMS: number;
	} = $props();

	function windows(value: CodexProfileQuota | null): { key: string; window: CodexQuotaWindow }[] {
		const limit = value?.snapshot?.rate_limit;
		const result: { key: string; window: CodexQuotaWindow }[] = [];
		if (limit?.primary_window) result.push({ key: "primary", window: limit.primary_window });
		if (limit?.secondary_window) result.push({ key: "secondary", window: limit.secondary_window });
		return result;
	}

	function formatPercent(value: number): string {
		return Number.isInteger(value) ? String(value) : value.toFixed(1);
	}

	function progressValue(value: number): number {
		return Math.min(100, Math.max(0, value));
	}

	function progressLabel(window: CodexQuotaWindow): string {
		return `${formatWindow(window.limit_window_seconds)} · ${$_("quota.remaining", { values: { value: formatPercent(window.remaining_percent) } })}`;
	}

	function formatWindow(seconds: number): string {
		if (seconds > 0 && seconds % 604800 === 0) return $_("quota.windowWeeks", { values: { count: seconds / 604800 } });
		if (seconds > 0 && seconds % 86400 === 0) return $_("quota.windowDays", { values: { count: seconds / 86400 } });
		if (seconds > 0 && seconds % 3600 === 0) return $_("quota.windowHours", { values: { count: seconds / 3600 } });
		return $_("quota.windowMinutes", { values: { count: Math.max(1, Math.round(seconds / 60)) } });
	}

	function isToday(value: Date, now: Date): boolean {
		return value.getFullYear() === now.getFullYear()
			&& value.getMonth() === now.getMonth()
			&& value.getDate() === now.getDate();
	}

	function formatReset(unixSeconds: number): string {
		if (!unixSeconds) return "—";
		const date = new Date(unixSeconds * 1000);
		const now = new Date(nowUnixMS);
		const options: Intl.DateTimeFormatOptions = isToday(date, now)
			? { hour: "2-digit", minute: "2-digit" }
			: {
				...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" as const }),
				month: "short",
				day: "numeric",
				hour: "2-digit",
				minute: "2-digit",
			};
		return date.toLocaleString(currentDesktopLocale(), options);
	}

	function formatQuotaValue(value: number | null | undefined, unit = ""): string {
		return formatQuotaAmount(value, unit, currentDesktopLocale());
	}

	function apiKeyStateLabel(value: string): string {
		switch (value) {
			case "active": return $_("quota.apiServiceActive");
			case "quota_exhausted": return $_("quota.apiServiceExhausted");
			case "expired": return $_("quota.apiServiceExpired");
			default: return $_("quota.apiServiceUnknown");
		}
	}

	function apiKeyStateDestructive(value: string): boolean {
		return value === "quota_exhausted" || value === "expired";
	}

	function apiWindowLabel(value: string): string {
		const key = `quota.apiWindow${value.charAt(0).toUpperCase()}${value.slice(1)}`;
		return $_(key);
	}

	function quotaStatusLabel(value: CodexProfileQuota): string {
		if (value.source === "sub2api") {
			if (value.status === "auth_required") return $_("quota.apiServiceAuthRequired");
			if (value.status === "unsupported") return $_("quota.apiServiceUnsupported");
			return $_("quota.apiServiceUnavailable");
		}
		if (value.status === "auth_required") return $_("quota.authRequired");
		if (value.status === "unsupported") return $_("quota.unsupported");
		return $_("quota.unavailable");
	}

</script>

{#if loading && !quota}
	<Skeleton class="h-12 w-full" />
{:else if quota?.status === "available" && quota.source === "sub2api" && quota.sub2api_snapshot}
	{@const snapshot = quota.sub2api_snapshot}
	<div class="flex min-w-0 flex-col gap-2">
		<div class="flex min-w-0 flex-wrap items-center justify-between gap-2">
			<span class="truncate text-sm font-medium">
				{$_("quota.apiServiceRemaining")}: {snapshot.unlimited ? $_("quota.unlimited") : formatQuotaValue(snapshot.remaining ?? snapshot.balance, snapshot.unit)}
			</span>
			<Badge variant={apiKeyStateDestructive(snapshot.key_state) ? "destructive" : "secondary"}>{apiKeyStateLabel(snapshot.key_state)}</Badge>
		</div>
		{#if snapshot.windows?.length}
			<div class="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(7rem,1fr))] gap-x-4 gap-y-2">
				{#each snapshot.windows as window (window.id)}
					{@render Sub2APIWindowSummary(window, snapshot)}
				{/each}
			</div>
		{/if}
	</div>
{:else if quota?.status === "available" && windows(quota).length > 0}
	<div class="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(7rem,1fr))] gap-x-4 gap-y-2">
		{#each windows(quota) as item (item.key)}
			<div class="flex min-w-0 flex-col gap-1">
				<span class="flex min-w-0 items-center justify-between gap-2 text-xs">
					<span class="truncate text-muted-foreground">{formatWindow(item.window.limit_window_seconds)}</span>
					<span class={cn("shrink-0 font-medium", item.window.remaining_percent <= 0 && "text-destructive")}>
						{$_("quota.remaining", { values: { value: formatPercent(item.window.remaining_percent) } })}
					</span>
				</span>
				<Progress
					value={progressValue(item.window.remaining_percent)}
					aria-label={progressLabel(item.window)}
				/>
				<span class="truncate text-xs text-muted-foreground">
					{$_("quota.resetsAt", { values: { value: formatReset(item.window.reset_at_unix_seconds) } })}
				</span>
			</div>
		{/each}
	</div>
	{:else if quota?.status === "auth_required"}
		<div><Badge variant="destructive">{quotaStatusLabel(quota)}</Badge></div>
	{:else if quota?.status === "unsupported"}
		<div><Badge variant="secondary">{quotaStatusLabel(quota)}</Badge></div>
{:else if quota?.status === "available"}
	<div><Badge variant="secondary">{$_("quota.noWindows")}</Badge></div>
	{:else if quota}
		<div><Badge variant="secondary">{quotaStatusLabel(quota)}</Badge></div>
{/if}

{#snippet Sub2APIWindowSummary(window: CodexSub2APIQuotaWindow, snapshot: CodexSub2APIQuotaSnapshot)}
	<div class="flex min-w-0 flex-col gap-1">
		<span class="flex min-w-0 items-center justify-between gap-2 text-xs">
			<span class="truncate text-muted-foreground">{apiWindowLabel(window.id)}</span>
			<span class={cn("shrink-0 font-medium", window.remaining_percent <= 0 && "text-destructive")}>{formatPercent(window.remaining_percent)}%</span>
		</span>
		<Progress value={progressValue(window.remaining_percent)} aria-label={$_("quota.remaining", { values: { value: formatPercent(window.remaining_percent) } })} />
		<span class="truncate text-xs text-muted-foreground">
			{formatQuotaValue(window.remaining, snapshot.unit)}
			{#if window.reset_at_unix_seconds != null}<span aria-hidden="true"> · </span>{formatReset(window.reset_at_unix_seconds)}{/if}
		</span>
	</div>
{/snippet}
