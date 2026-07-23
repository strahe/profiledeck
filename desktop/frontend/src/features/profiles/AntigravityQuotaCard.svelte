<script lang="ts">
	import { _ } from "svelte-i18n";
	import GaugeIcon from "@lucide/svelte/icons/gauge";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import IconAction from "$lib/components/app/IconAction.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as Card from "$lib/components/ui/card";
	import * as Empty from "$lib/components/ui/empty";
	import { Progress } from "$lib/components/ui/progress";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { currentDesktopLocale } from "$lib/i18n";
	import { cn } from "$lib/utils";

	import type {
		AntigravityProfileQuota,
		AntigravityQuotaBucket,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/antigravity/models";
	import ProfileQuotaFreshness from "./ProfileQuotaFreshness.svelte";
	import type { AntigravityQuotaCheckOutcome } from "./antigravity-quota.svelte.js";

	let {
		quota,
		loading,
		active,
		checkedAtUnixMS,
		checkOutcome,
		nowUnixMS,
		onRefresh,
	}: {
		quota: AntigravityProfileQuota | null;
		loading: boolean;
		active: boolean;
		checkedAtUnixMS: number;
		checkOutcome: AntigravityQuotaCheckOutcome;
		nowUnixMS: number;
		onRefresh: () => void;
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

	function formatTimestamp(unixSeconds: number): string {
		return new Date(unixSeconds * 1000).toLocaleString(currentDesktopLocale(), {
			month: "short",
			day: "numeric",
			hour: "2-digit",
			minute: "2-digit",
		});
	}

	function formatFetchedAt(unixMS: number): string {
		return new Date(unixMS).toLocaleString(currentDesktopLocale(), {
			month: "short",
			day: "numeric",
			hour: "2-digit",
			minute: "2-digit",
		});
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>{$_("antigravity.quota.title")}</Card.Title>
		<Card.Description>{$_("antigravity.quota.description")}</Card.Description>
		<Card.Action>
			<div class="flex items-center gap-2">
				<ProfileQuotaFreshness {checkedAtUnixMS} {checkOutcome} {nowUnixMS} />
				{#if active}
					<IconAction label={$_("actions.refreshQuota")} disabled={loading} onclick={onRefresh}>
						{#if loading}<Spinner />{:else}<RefreshCwIcon />{/if}
					</IconAction>
				{/if}
			</div>
		</Card.Action>
	</Card.Header>
	<Card.Content class="flex flex-col gap-4">
		<Alert.Root>
			<TriangleAlertIcon data-icon="inline-start" />
			<Alert.Title>{$_("antigravity.quota.riskTitle")}</Alert.Title>
			<Alert.Description>{$_("antigravity.quota.riskDescription")}</Alert.Description>
		</Alert.Root>
		{#if loading && !quota?.snapshot}
			<div class="flex flex-col gap-4">
				<Skeleton class="h-5 w-28" />
				<div class="grid gap-3 sm:grid-cols-2"><Skeleton class="h-28 w-full" /><Skeleton class="h-28 w-full" /></div>
			</div>
		{:else if !quota?.snapshot && !quota}
			<Empty.Root class="border-0 py-6">
				<Empty.Header>
					<Empty.Media variant="icon"><GaugeIcon /></Empty.Media>
					<Empty.Title>{active ? $_("antigravity.quota.notLoadedTitle") : $_("antigravity.quota.noSnapshotTitle")}</Empty.Title>
					<Empty.Description>{active ? $_("antigravity.quota.notLoadedDescription") : $_("antigravity.quota.noSnapshotDescription")}</Empty.Description>
				</Empty.Header>
			</Empty.Root>
			{:else if !quota?.snapshot}
				<Alert.Root variant={quota?.status === "auth_required" ? "destructive" : "default"}>
				<TriangleAlertIcon data-icon="inline-start" />
				<Alert.Title>{quota?.status === "auth_required" ? $_("antigravity.quota.authRequired") : quota?.status === "inactive" ? $_("antigravity.quota.inactive") : $_("antigravity.quota.unavailable")}</Alert.Title>
					<Alert.Description>{quota?.status === "auth_required" ? $_("antigravity.quota.authRequiredDescription") : quota?.status === "inactive" ? $_("antigravity.quota.inactiveDescription") : $_("antigravity.quota.unavailableDescription")}</Alert.Description>
				</Alert.Root>
			{:else}
				{@const groups = quota.snapshot.groups ?? []}
				<div class="flex flex-col gap-5">
				{#if quota.status === "inactive"}
					<Alert.Root>
						<GaugeIcon data-icon="inline-start" />
						<Alert.Title>{$_("antigravity.quota.sessionSnapshotTitle")}</Alert.Title>
						<Alert.Description>{$_("antigravity.quota.sessionSnapshotDescription")}</Alert.Description>
					</Alert.Root>
				{:else if quota.status !== "available"}
					<Alert.Root variant={quota.status === "auth_required" ? "destructive" : "default"}>
						<TriangleAlertIcon data-icon="inline-start" />
						<Alert.Title>{quota.status === "auth_required" ? $_("antigravity.quota.authRequired") : $_("antigravity.quota.unavailable")}</Alert.Title>
						<Alert.Description>{quota.status === "auth_required" ? $_("antigravity.quota.staleAuthDescription") : $_("antigravity.quota.staleUnavailableDescription")}</Alert.Description>
					</Alert.Root>
				{:else if !active}
					<Alert.Root>
						<GaugeIcon data-icon="inline-start" />
						<Alert.Title>{$_("antigravity.quota.sessionSnapshotTitle")}</Alert.Title>
						<Alert.Description>{$_("antigravity.quota.sessionSnapshotDescription")}</Alert.Description>
					</Alert.Root>
				{/if}

				<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
					<span>{$_("antigravity.quota.fetchedAt", { values: { value: formatFetchedAt(quota.snapshot.fetched_at_unix_ms) } })}</span>
				</div>

				{#if groups.length === 0}
					<Empty.Root class="border-0 py-6">
						<Empty.Header>
							<Empty.Media variant="icon"><GaugeIcon /></Empty.Media>
							<Empty.Title>{$_("antigravity.quota.noDisplayableTitle")}</Empty.Title>
							<Empty.Description>{$_("antigravity.quota.noDisplayableDescription")}</Empty.Description>
						</Empty.Header>
					</Empty.Root>
				{:else}
					<div class="flex flex-col gap-4">
						{#each groups as group, groupIndex (`${group.display_name}:${groupIndex}`)}
							<section class="flex flex-col gap-3 rounded-lg border p-3">
								<h3 class="text-sm font-medium">{group.display_name}</h3>
								<div class="grid gap-3 sm:grid-cols-2">
									{#each group.buckets as bucket, bucketIndex (`${bucket.bucket_id}:${bucketIndex}`)}
										<div class="flex min-w-0 flex-col gap-3 rounded-lg border p-3">
											<div class="flex items-center justify-between gap-2">
												<span class="text-xs font-medium text-muted-foreground">{windowLabel(bucket.window)}</span>
												<span class={cn("text-sm font-medium", bucket.remaining_percent <= 0 && "text-destructive")}>{formatPercent(bucket.remaining_percent)}%</span>
											</div>
											<Progress value={progressValue(bucket.remaining_percent)} aria-label={progressLabel(bucket)} />
											<span class="text-xs text-muted-foreground">{$_("antigravity.quota.resetsAt", { values: { value: formatTimestamp(bucket.reset_at_unix_seconds) } })}</span>
										</div>
									{/each}
								</div>
							</section>
						{/each}
					</div>
				{/if}
			</div>
		{/if}
	</Card.Content>
</Card.Root>
