<script lang="ts">
	import { _ } from "svelte-i18n";

	import { Badge } from "$lib/components/ui/badge";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { currentDesktopLocale } from "$lib/i18n";

	import type { CodexProfileQuota, CodexQuotaWindow } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	let { quota, loading }: { quota: CodexProfileQuota | null; loading: boolean } = $props();

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

	function formatWindow(seconds: number): string {
		if (seconds > 0 && seconds % 604800 === 0) return $_("quota.windowWeeks", { values: { count: seconds / 604800 } });
		if (seconds > 0 && seconds % 86400 === 0) return $_("quota.windowDays", { values: { count: seconds / 86400 } });
		if (seconds > 0 && seconds % 3600 === 0) return $_("quota.windowHours", { values: { count: seconds / 3600 } });
		return $_("quota.windowMinutes", { values: { count: Math.max(1, Math.round(seconds / 60)) } });
	}

	function formatReset(unixSeconds: number): string {
		if (!unixSeconds) return "—";
		const date = new Date(unixSeconds * 1000);
		const delta = date.getTime() - Date.now();
		const options: Intl.DateTimeFormatOptions = delta >= 0 && delta < 24 * 60 * 60 * 1000
			? { hour: "2-digit", minute: "2-digit" }
			: { month: "short", day: "numeric" };
		return date.toLocaleString(currentDesktopLocale(), options);
	}
</script>

{#if loading}
	<Skeleton class="h-5 w-52" />
{:else if !quota}
	<span class="text-xs text-muted-foreground">{$_("quota.notLoaded")}</span>
{:else if quota.status === "available" && windows(quota).length > 0}
	<div class="flex flex-wrap items-center gap-1.5">
		{#each windows(quota) as item (item.key)}
			<Badge variant="outline">
				{formatWindow(item.window.limit_window_seconds)} · {$_("quota.remaining", { values: { value: formatPercent(item.window.remaining_percent) } })} · {formatReset(item.window.reset_at_unix_seconds)}
			</Badge>
		{/each}
	</div>
{:else if quota.status === "auth_required"}
	<Badge variant="destructive">{$_("quota.authRequired")}</Badge>
{:else if quota.status === "unsupported"}
	<Badge variant="secondary">{$_("quota.unsupported")}</Badge>
{:else if quota.status === "available"}
	<Badge variant="secondary">{$_("quota.noWindows")}</Badge>
{:else}
	<Badge variant="secondary">{$_("quota.unavailable")}</Badge>
{/if}
