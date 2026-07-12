<script lang="ts">
	import { _ } from "svelte-i18n";
	import GaugeIcon from "@lucide/svelte/icons/gauge";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";

	import IconAction from "$lib/components/app/IconAction.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as Card from "$lib/components/ui/card";
	import * as Empty from "$lib/components/ui/empty";
	import { Badge } from "$lib/components/ui/badge";
	import { Progress } from "$lib/components/ui/progress";
	import { Separator } from "$lib/components/ui/separator";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { currentDesktopLocale } from "$lib/i18n";

	import type {
		CodexProfileQuota,
		CodexQuotaRateLimit,
		CodexQuotaWindow,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	let {
		quota,
		loading,
		disabled,
		onRefresh,
	}: {
		quota: CodexProfileQuota | null;
		loading: boolean;
		disabled: boolean;
		onRefresh: () => void;
	} = $props();

	function formatPercent(value: number): string {
		return Number.isInteger(value) ? String(value) : value.toFixed(1);
	}

	function formatWindow(seconds: number): string {
		if (seconds > 0 && seconds % 604800 === 0) return $_("quota.windowWeeks", { values: { count: seconds / 604800 } });
		if (seconds > 0 && seconds % 86400 === 0) return $_("quota.windowDays", { values: { count: seconds / 86400 } });
		if (seconds > 0 && seconds % 3600 === 0) return $_("quota.windowHours", { values: { count: seconds / 3600 } });
		return $_("quota.windowMinutes", { values: { count: Math.max(1, Math.round(seconds / 60)) } });
	}

	function formatTimestamp(unixSeconds: number): string {
		if (!unixSeconds) return "—";
		return new Date(unixSeconds * 1000).toLocaleString(currentDesktopLocale(), {
			month: "short",
			day: "numeric",
			hour: "2-digit",
			minute: "2-digit",
		});
	}

	function formatFetchedAt(unixMS: number): string {
		if (!unixMS) return "—";
		return new Date(unixMS).toLocaleString(currentDesktopLocale(), {
			month: "short",
			day: "numeric",
			hour: "2-digit",
			minute: "2-digit",
		});
	}

	function formatPlanType(value: string): string {
		return value
			.split(/[_-]+/)
			.filter(Boolean)
			.map((part) => part.charAt(0).toUpperCase() + part.slice(1))
			.join(" ");
	}

	function limitBlocked(limit: CodexQuotaRateLimit): boolean {
		return !limit.allowed || limit.limit_reached;
	}

	function formatAmountPercent(value: string, percent: number): string {
		const formattedPercent = `${formatPercent(percent)}%`;
		return value ? `${value} · ${formattedPercent}` : formattedPercent;
	}
</script>

<Card.Root class="lg:col-span-2">
	<Card.Header>
		<Card.Title>{$_("quota.title")}</Card.Title>
		<Card.Description>{$_("quota.description")}</Card.Description>
		<Card.Action>
			<IconAction
				label={$_("actions.refreshQuota")}
				disabled={disabled || loading}
				onclick={onRefresh}
			>
				{#if loading}<Spinner />{:else}<RefreshCwIcon />{/if}
			</IconAction>
		</Card.Action>
	</Card.Header>
	<Card.Content>
		{#if loading && !quota}
			<div class="flex flex-col gap-4">
				<div class="flex items-center gap-2"><Skeleton class="h-5 w-16" /><Skeleton class="h-5 w-24" /></div>
				<div class="grid gap-3 sm:grid-cols-2"><Skeleton class="h-32 w-full" /><Skeleton class="h-32 w-full" /></div>
			</div>
		{:else if !quota}
			<Empty.Root class="border-0 py-6">
				<Empty.Header>
					<Empty.Media variant="icon"><GaugeIcon /></Empty.Media>
					<Empty.Title>{$_("quota.notLoadedTitle")}</Empty.Title>
					<Empty.Description>{$_("quota.notLoadedDescription")}</Empty.Description>
				</Empty.Header>
			</Empty.Root>
		{:else if quota.status !== "available" || !quota.snapshot}
			<Alert.Root variant={quota.status === "auth_required" ? "destructive" : "default"}>
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>
					{quota.status === "auth_required" ? $_("quota.authRequired") : quota.status === "unsupported" ? $_("quota.unsupported") : $_("quota.unavailable")}
				</Alert.Title>
				<Alert.Description>
					{quota.status === "auth_required" ? $_("quota.authRequiredDescription") : quota.status === "unsupported" ? $_("quota.unsupportedDescription") : $_("quota.unavailableDescription")}
				</Alert.Description>
			</Alert.Root>
		{:else}
			{@const snapshot = quota.snapshot}
			<div class="flex flex-col gap-5">
				<div class="flex flex-wrap items-center gap-2">
					{#if snapshot.plan_type}<Badge variant="outline">{formatPlanType(snapshot.plan_type)}</Badge>{/if}
					{#if snapshot.rate_limit}
						<Badge variant={limitBlocked(snapshot.rate_limit) ? "destructive" : "secondary"}>
							{limitBlocked(snapshot.rate_limit) ? $_("quota.limitReached") : $_("quota.available")}
						</Badge>
					{/if}
					<span class="text-xs text-muted-foreground">{$_("quota.fetchedAt", { values: { value: formatFetchedAt(snapshot.fetched_at_unix_ms) } })}</span>
				</div>

				{#if snapshot.rate_limit}
					{@render RateLimitWindows(snapshot.rate_limit)}
				{:else}
					<p class="text-sm text-muted-foreground">{$_("quota.noWindows")}</p>
				{/if}

				{#if snapshot.credits || snapshot.spend_control || snapshot.reset_credits_available_count != null}
					<Separator />
					<dl class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
						{#if snapshot.credits}
							<div class="flex min-w-0 flex-col gap-1">
								<dt class="text-xs text-muted-foreground">{$_("quota.credits")}</dt>
								<dd class="truncate text-sm">
									{snapshot.credits.unlimited ? $_("quota.unlimited") : snapshot.credits.balance ?? (snapshot.credits.has_credits ? $_("quota.available") : $_("quota.none"))}
								</dd>
							</div>
						{/if}
						{#if snapshot.spend_control}
							<div class="flex min-w-0 flex-col gap-1">
								<dt class="text-xs text-muted-foreground">{$_("quota.spendControl")}</dt>
								<dd class="truncate text-sm">{snapshot.spend_control.reached ? $_("quota.limitReached") : $_("quota.available")}</dd>
							</div>
						{/if}
						{#if snapshot.reset_credits_available_count != null}
							<div class="flex min-w-0 flex-col gap-1">
								<dt class="text-xs text-muted-foreground">{$_("quota.earnedResets")}</dt>
								<dd class="text-sm">{snapshot.reset_credits_available_count}</dd>
							</div>
						{/if}
					</dl>
				{/if}

				{#if snapshot.spend_control?.individual_limit}
					{@const limit = snapshot.spend_control.individual_limit}
					<Separator />
					<div class="flex flex-col gap-3">
						<h3 class="text-sm font-medium">{$_("quota.individualSpendLimit")}</h3>
						<dl class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
							{@render Detail($_("quota.limit"), limit.limit)}
							{@render Detail($_("quota.usedValue"), formatAmountPercent(limit.used, limit.used_percent))}
							{@render Detail($_("quota.remainingValue"), formatAmountPercent(limit.remaining, limit.remaining_percent))}
							{@render Detail($_("quota.reset"), formatTimestamp(limit.reset_at_unix_seconds))}
						</dl>
					</div>
				{/if}

				{#if snapshot.additional_rate_limits?.length}
					<Separator />
					<div class="flex flex-col gap-4">
						<h3 class="text-sm font-medium">{$_("quota.additionalLimits")}</h3>
						{#each snapshot.additional_rate_limits as limit, index (`${limit.id}:${index}`)}
							<div class="flex flex-col gap-2">
								<div class="flex items-center gap-2">
									<span class="text-sm font-medium">{limit.name || $_("quota.additionalLimit")}</span>
									<Badge variant={limitBlocked(limit) ? "destructive" : "secondary"}>{limitBlocked(limit) ? $_("quota.limitReached") : $_("quota.available")}</Badge>
								</div>
								{@render RateLimitWindows(limit)}
							</div>
						{/each}
					</div>
				{/if}
			</div>
		{/if}
	</Card.Content>
</Card.Root>

{#snippet RateLimitWindows(limit: CodexQuotaRateLimit)}
	{#if limit.primary_window || limit.secondary_window}
		<div class="grid gap-3 sm:grid-cols-2">
			{#if limit.primary_window}{@render WindowPanel(limit.primary_window)}{/if}
			{#if limit.secondary_window}{@render WindowPanel(limit.secondary_window)}{/if}
		</div>
	{:else}
		<p class="text-sm text-muted-foreground">{$_("quota.noWindows")}</p>
	{/if}
{/snippet}

{#snippet WindowPanel(window: CodexQuotaWindow)}
	<div class="flex flex-col gap-3 rounded-lg border p-3">
		<div class="flex items-center justify-between gap-2">
			<span class="text-xs font-medium text-muted-foreground">{formatWindow(window.limit_window_seconds)}</span>
			<span class="text-sm font-medium">{formatPercent(window.remaining_percent)}%</span>
		</div>
		<Progress value={window.remaining_percent} aria-label={$_("quota.remaining", { values: { value: formatPercent(window.remaining_percent) } })} />
		<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
			<span>{$_("quota.used", { values: { value: formatPercent(window.used_percent) } })}</span>
			<span>{$_("quota.resetsAt", { values: { value: formatTimestamp(window.reset_at_unix_seconds) } })}</span>
		</div>
	</div>
{/snippet}

{#snippet Detail(label: string, value: string)}
	<div class="flex min-w-0 flex-col gap-1">
		<dt class="text-xs text-muted-foreground">{label}</dt>
		<dd class="truncate text-sm">{value || "—"}</dd>
	</div>
{/snippet}
