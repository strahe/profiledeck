<script lang="ts">
	import { _ } from "svelte-i18n";

	import { Badge } from "$lib/components/ui/badge";
	import { Progress } from "$lib/components/ui/progress";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { currentDesktopLocale } from "$lib/i18n";
	import { cn } from "$lib/utils";

	import type {
		AntigravityProfileQuota,
		AntigravityQuotaBucket,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/antigravity/models";

	let {
		quota,
		loading,
		nowUnixMS,
	}: {
		quota: AntigravityProfileQuota | null;
		loading: boolean;
		nowUnixMS: number;
	} = $props();

	function formatPercent(value: number): string {
		return Number.isInteger(value) ? String(value) : value.toFixed(1);
	}

	function progressValue(value: number): number {
		return Math.min(100, Math.max(0, value));
	}

	function windowLabel(window: string): string {
		return window === "5h" ? $_("antigravity.quota.window5h") : $_("antigravity.quota.windowWeekly");
	}

	function progressLabel(bucket: AntigravityQuotaBucket): string {
		return `${windowLabel(bucket.window)} · ${$_("antigravity.quota.remaining", { values: { value: formatPercent(bucket.remaining_percent) } })}`;
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
				month: "short",
				day: "numeric",
				hour: "2-digit",
				minute: "2-digit",
			};
		return date.toLocaleString(currentDesktopLocale(), options);
	}
</script>

{#if loading && !quota?.snapshot}
	<Skeleton class="h-14 w-full" />
{:else if quota?.snapshot}
	{@const groups = quota.snapshot.groups ?? []}
	<div class="flex min-w-0 flex-col gap-2">
		{#if quota.status !== "available"}
			<div>
				<Badge variant={quota.status === "auth_required" ? "destructive" : "secondary"}>
					{quota.status === "auth_required" ? $_("antigravity.quota.authRequired") : quota.status === "inactive" ? $_("antigravity.quota.inactive") : $_("antigravity.quota.unavailable")}
				</Badge>
			</div>
		{/if}
		{#if groups.length === 0}
			<div><Badge variant="secondary">{$_("antigravity.quota.noDisplayableTitle")}</Badge></div>
		{:else}
			{#each groups as group, groupIndex (`${group.display_name}:${groupIndex}`)}
				<div class="grid min-w-0 gap-2 sm:grid-cols-[minmax(8rem,0.75fr)_minmax(0,2fr)] sm:items-center">
					<span class="truncate text-xs font-medium text-muted-foreground">{group.display_name}</span>
					<div class="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(7rem,1fr))] gap-x-4 gap-y-2">
						{#each group.buckets as bucket, bucketIndex (`${bucket.bucket_id}:${bucketIndex}`)}
							<div class="flex min-w-0 flex-col gap-1">
								<span class="flex min-w-0 items-center justify-between gap-2 text-xs">
									<span class="truncate text-muted-foreground">{windowLabel(bucket.window)}</span>
									<span class={cn("shrink-0 font-medium", bucket.remaining_percent <= 0 && "text-destructive")}>
										{$_("antigravity.quota.remaining", { values: { value: formatPercent(bucket.remaining_percent) } })}
									</span>
								</span>
								<Progress value={progressValue(bucket.remaining_percent)} aria-label={progressLabel(bucket)} />
								<span class="truncate text-xs text-muted-foreground">
									{$_("antigravity.quota.resetsAt", { values: { value: formatReset(bucket.reset_at_unix_seconds) } })}
								</span>
							</div>
						{/each}
					</div>
				</div>
			{/each}
		{/if}
	</div>
{:else if quota?.status === "auth_required"}
	<div><Badge variant="destructive">{$_("antigravity.quota.authRequired")}</Badge></div>
{:else if quota?.status === "inactive"}
	<div><Badge variant="secondary">{$_("antigravity.quota.inactive")}</Badge></div>
{:else if quota?.status === "unavailable"}
	<div><Badge variant="secondary">{$_("antigravity.quota.unavailable")}</Badge></div>
{/if}
