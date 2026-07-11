<script lang="ts">
	import { onMount } from "svelte";
	import { Events, type CancellablePromise } from "@wailsio/runtime";
	import { _ } from "svelte-i18n";

	import { UsageService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { UsageAutoSyncStatus } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend/models";
	import type { UsageReportResult } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import * as Accordion from "$lib/components/ui/accordion";
	import * as Alert from "$lib/components/ui/alert";
	import { Badge } from "$lib/components/ui/badge";
	import * as Card from "$lib/components/ui/card";
	import * as Empty from "$lib/components/ui/empty";
	import { Spinner } from "$lib/components/ui/spinner";
	import * as ToggleGroup from "$lib/components/ui/toggle-group";
	import { desktopErrorMessage, isCancelError } from "$lib/desktop-errors";
	import { currentDesktopLocale, translate } from "$lib/i18n";
	import UsageModelTable from "./UsageModelTable.svelte";
	import UsageTrendChart from "./UsageTrendChart.svelte";

	type UsageRange = "today" | "7d" | "30d" | "all";
	type UsageMetric = "cost" | "tokens";
	type Props = {
		showError: (value: unknown) => void;
	};

	let { showError }: Props = $props();
	let range = $state<UsageRange>("7d");
	let rangeSelection = $state<string>("7d");
	let metric = $state<UsageMetric>("cost");
	let metricSelection = $state<string>("cost");
	let report = $state<UsageReportResult | null>(null);
	let autoSyncStatus = $state<UsageAutoSyncStatus | null>(null);
	let loading = $state(true);
	let reportError = $state("");
	let reportRequest: CancellablePromise<UsageReportResult> | null = null;
	let statusRequest: CancellablePromise<UsageAutoSyncStatus> | null = null;
	let pendingReportRefresh = false;
	let disposed = false;

	onMount(() => {
		disposed = false;
		void refreshReport(range, true);
		void refreshAutoSyncStatus();
		const offUsageSyncStatus = Events.On("profiledeck:usage-sync-status", (event) => {
			const status = event.data as UsageAutoSyncStatus | null;
			if (!status) return;
			autoSyncStatus = status;
			if (status.outcome === "success" || status.outcome === "warning") {
				void refreshReport(range, false, true);
			}
		});
		return () => {
			disposed = true;
			pendingReportRefresh = false;
			offUsageSyncStatus();
			reportRequest?.cancel("usage page unmounted");
			statusRequest?.cancel("usage page unmounted");
		};
	});

	async function refreshAutoSyncStatus() {
		statusRequest?.cancel("usage status replaced");
		const request = UsageService.AutoSyncStatus();
		statusRequest = request;
		try {
			const status = await request;
			if (statusRequest === request) autoSyncStatus = status;
		} catch (error) {
			if (statusRequest === request && !isCancelError(error)) showError(error);
		} finally {
			if (statusRequest === request) statusRequest = null;
		}
	}

	async function refreshReport(nextRange: UsageRange, announceError: boolean, background = false) {
		if (disposed) return;
		if (reportRequest) {
			if (background) {
				pendingReportRefresh = true;
				return;
			}
			pendingReportRefresh = false;
			reportRequest.cancel("usage report replaced");
		}
		if (report?.range.preset !== nextRange) report = null;
		reportError = "";
		loading = true;
		const request = UsageService.Report("codex", nextRange);
		reportRequest = request;
		try {
			const next = await request;
			if (reportRequest !== request) return;
			report = next;
			reportError = "";
		} catch (error) {
			if (reportRequest !== request || isCancelError(error)) return;
			reportError = desktopErrorMessage(error, translate("errors.desktopUnavailable"));
			if (announceError) showError(error);
		} finally {
			if (reportRequest === request) {
				reportRequest = null;
				loading = false;
				if (!disposed && pendingReportRefresh) {
					pendingReportRefresh = false;
					void refreshReport(range, false, true);
				}
			}
		}
	}

	function changeRange(value: string) {
		if (!isUsageRange(value)) {
			rangeSelection = range;
			return;
		}
		rangeSelection = value;
		if (value === range) return;
		range = value;
		void refreshReport(value, true);
	}

	function changeMetric(value: string) {
		if (value !== "cost" && value !== "tokens") {
			metricSelection = metric;
			return;
		}
		metricSelection = value;
		metric = value;
	}

	function isUsageRange(value: string): value is UsageRange {
		return value === "today" || value === "7d" || value === "30d" || value === "all";
	}

	function formatInteger(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale()).format(value);
	}

	function formatCompact(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale(), {
			notation: "compact",
			maximumFractionDigits: 2,
		}).format(value);
	}

	function formatPercent(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale(), {
			style: "percent",
			maximumFractionDigits: 1,
		}).format(value);
	}

	function formatCurrency(value: string): string {
		const parsed = Number(value);
		if (!Number.isFinite(parsed)) return value;
		return new Intl.NumberFormat(currentDesktopLocale(), {
			style: "currency",
			currency: "USD",
			minimumFractionDigits: 2,
			maximumFractionDigits: 6,
		}).format(parsed);
	}

	function formatLastSync(unixMS: number): string {
		if (unixMS <= 0) return translate("usage.neverSynced");
		const value = new Date(unixMS);
		const now = new Date();
		const sameDay = value.getFullYear() === now.getFullYear() && value.getMonth() === now.getMonth() && value.getDate() === now.getDate();
		return new Intl.DateTimeFormat(currentDesktopLocale(), sameDay ? {
			timeStyle: "short",
		} : {
			dateStyle: "short",
			timeStyle: "short",
		}).format(value);
	}

	function lastSuccessfulSync(): number {
		return autoSyncStatus?.last_success_at_unix_ms || report?.import.last_synced_at_unix_ms || 0;
	}

	function dataQualityIssueCount(value: UsageReportResult): number {
		return Number(autoSyncStatus?.outcome === "warning")
			+ Number(value.import.invalid_lines > 0 || value.import.unsupported_lines > 0)
			+ Number(value.summary.undated_event_count > 0)
			+ Number(value.summary.partial_cost_event_count > 0)
			+ Number(value.summary.event_count > 0 && value.summary.unknown_cost_event_count > 0);
	}
</script>

<ContentContainer class="max-w-6xl">
	<PageHeader title={$_("usage.title")}>
		{#snippet meta()}
			<div class="flex items-center gap-1.5 text-xs text-muted-foreground">
				{#if autoSyncStatus?.syncing}<Spinner />{/if}
				<span class={autoSyncStatus?.error ? "text-destructive" : ""}>
					{autoSyncStatus?.error
						? $_("usage.autoSync.failedDescription")
						: $_("usage.autoSync.summary", { values: { seconds: autoSyncStatus?.interval_seconds ?? 15, value: formatLastSync(lastSuccessfulSync()) } })}
				</span>
			</div>
		{/snippet}
		{#snippet actions()}
			<ToggleGroup.Root type="single" bind:value={rangeSelection} onValueChange={changeRange} variant="outline" size="sm" aria-label={$_("usage.range.label")}>
				<ToggleGroup.Item value="today">{$_("usage.range.today")}</ToggleGroup.Item>
				<ToggleGroup.Item value="7d">{$_("usage.range.sevenDays")}</ToggleGroup.Item>
				<ToggleGroup.Item value="30d">{$_("usage.range.thirtyDays")}</ToggleGroup.Item>
				<ToggleGroup.Item value="all">{$_("usage.range.all")}</ToggleGroup.Item>
			</ToggleGroup.Root>
		{/snippet}
	</PageHeader>

	{#if reportError}
		<Alert.Root variant="destructive">
			<Alert.Title>{$_("usage.loadFailedTitle")}</Alert.Title>
			<Alert.Description>{reportError}</Alert.Description>
		</Alert.Root>
	{/if}

	{#if loading && !report}
		<div class="grid min-h-56 place-items-center rounded-lg border bg-card"><Spinner class="size-5" /></div>
	{:else if report}
		{#if autoSyncStatus?.outcome === "warning" || report.import.invalid_lines > 0 || report.import.unsupported_lines > 0 || report.summary.undated_event_count > 0 || report.summary.partial_cost_event_count > 0 || (report.summary.event_count > 0 && report.summary.unknown_cost_event_count > 0)}
			<Alert.Root>
				<Alert.Title>{$_("usage.dataQuality.title")}</Alert.Title>
				<Alert.Description>
					<p>{$_("usage.dataQuality.summary", { values: { count: dataQualityIssueCount(report) } })}</p>
					<Accordion.Root type="single" class="mt-1">
						<Accordion.Item value="details" class="border-0">
							<Accordion.Trigger class="py-1 text-xs">{$_("usage.dataQuality.showDetails")}</Accordion.Trigger>
							<Accordion.Content>
								<ul class="flex list-disc flex-col gap-1 pl-4">
									{#if autoSyncStatus?.outcome === "warning"}<li>{$_("usage.dataQuality.fileErrors", { values: { count: autoSyncStatus.import_error_count } })}</li>{/if}
									{#if report.import.invalid_lines > 0 || report.import.unsupported_lines > 0}<li>{$_("usage.dataQuality.lines", { values: { invalid: formatInteger(report.import.invalid_lines), unsupported: formatInteger(report.import.unsupported_lines) } })}</li>{/if}
									{#if report.summary.undated_event_count > 0}<li>{$_("usage.dataQuality.undated", { values: { count: formatInteger(report.summary.undated_event_count) } })}</li>{/if}
									{#if report.summary.partial_cost_event_count > 0}<li>{$_("usage.dataQuality.partialPricing", { values: { count: formatInteger(report.summary.partial_cost_event_count) } })}</li>{/if}
									{#if report.summary.event_count > 0 && report.summary.unknown_cost_event_count > 0}<li>{$_("usage.dataQuality.pricing", { values: { count: formatInteger(report.summary.unknown_cost_event_count), coverage: formatPercent(report.summary.pricing_coverage) } })}</li>{/if}
								</ul>
							</Accordion.Content>
						</Accordion.Item>
					</Accordion.Root>
				</Alert.Description>
			</Alert.Root>
		{/if}

		{#if report.summary.event_count === 0}
			<Empty.Root class="rounded-lg border bg-card py-12">
				<Empty.Header>
					<Empty.Title>{$_("usage.emptyTitle")}</Empty.Title>
					<Empty.Description>{$_("usage.emptyDescription")}</Empty.Description>
				</Empty.Header>
			</Empty.Root>
		{:else}
			<div class="grid gap-3 sm:grid-cols-3">
				<Card.Root>
					<Card.Header class="gap-1 py-3">
						<div class="flex items-center justify-between gap-2">
							<Card.Description>{$_("usage.apiEquivalentCost")}</Card.Description>
							{#if report.summary.cost_status !== "estimated"}<Badge variant="outline">{report.summary.pricing_coverage > 0 ? $_("usage.pricing.partial") : $_("usage.pricing.unknown")}</Badge>{/if}
						</div>
						<Card.Title class="text-xl tabular-nums">{formatCurrency(report.summary.known_estimated_cost_usd)}</Card.Title>
						<p class="text-xs text-muted-foreground">
							{#if report.summary.cost_status === "estimated"}
								{$_("usage.pricing.apiEstimate")}
							{:else if report.summary.cost_status === "partial"}
								{$_("usage.pricing.partialEstimate")}
							{:else}
								{$_("usage.pricing.coverage", { values: { coverage: formatPercent(report.summary.pricing_coverage) } })}
							{/if}
						</p>
					</Card.Header>
				</Card.Root>
				<Card.Root>
					<Card.Header class="gap-1 py-3">
						<Card.Description>{$_("usage.sessions")}</Card.Description>
						<Card.Title class="text-xl tabular-nums">{formatInteger(report.summary.session_count)}</Card.Title>
					</Card.Header>
				</Card.Root>
				<Card.Root>
					<Card.Header class="gap-1 py-3">
						<Card.Description>{$_("usage.totalTokens")}</Card.Description>
						<Card.Title class="text-xl tabular-nums">{formatCompact(report.summary.total_tokens)}</Card.Title>
					</Card.Header>
				</Card.Root>
			</div>

			<Card.Root>
				<Card.Header class="pb-2">
					<Card.Title class="text-sm">{$_("usage.trendTitle")}</Card.Title>
					<Card.Action>
						<ToggleGroup.Root type="single" bind:value={metricSelection} onValueChange={changeMetric} variant="outline" size="sm" aria-label={$_("usage.chart.metricLabel")}>
							<ToggleGroup.Item value="cost">{$_("usage.chart.cost")}</ToggleGroup.Item>
							<ToggleGroup.Item value="tokens">{$_("usage.chart.tokens")}</ToggleGroup.Item>
						</ToggleGroup.Root>
					</Card.Action>
				</Card.Header>
				<Card.Content><UsageTrendChart points={report.trend ?? []} range={report.range} {metric} /></Card.Content>
			</Card.Root>

			<Card.Root>
				<Card.Header class="pb-2">
					<Card.Title class="text-sm">{$_("usage.modelsTitle")}</Card.Title>
				</Card.Header>
				<Card.Content class="px-0 pb-0"><UsageModelTable models={report.models ?? []} /></Card.Content>
			</Card.Root>
		{/if}
	{/if}
</ContentContainer>
