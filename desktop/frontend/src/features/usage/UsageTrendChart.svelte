<script lang="ts">
	import { tick } from "svelte";
	import { _ } from "svelte-i18n";
	import type { UsageResolvedRange, UsageTrendPoint } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	import { currentDesktopLocale, translate } from "$lib/i18n";

	type UsageMetric = "cost" | "tokens";
	type TooltipSource = "pointer" | "focus";
	type TooltipState = {
		index: number;
		left: number;
		top: number;
		source: TooltipSource;
	};

	let { points, range, metric }: { points: UsageTrendPoint[]; range: UsageResolvedRange; metric: UsageMetric } = $props();

	const width = 720;
	const height = 240;
	const margin = { top: 14, right: 12, bottom: 38, left: 62 };
	const plotWidth = width - margin.left - margin.right;
	const plotHeight = height - margin.top - margin.bottom;
	const plotBottom = margin.top + plotHeight;
	const chartTitleID = "usage-trend-title";
	const chartDescriptionID = "usage-trend-description";
	const tooltipID = "usage-trend-tooltip";

	let containerElement = $state<HTMLDivElement>();
	let tooltipElement = $state<HTMLDivElement>();
	let activeIndex = $state(0);
	let focusedIndex = $state<number | null>(null);
	let tooltip = $state<TooltipState | null>(null);
	let tooltipRevision = 0;
	let tooltipPoint = $derived(tooltip === null ? null : points[tooltip.index] ?? null);
	let slotWidth = $derived(plotWidth / Math.max(points.length, 1));
	let barWidth = $derived(Math.min(34, Math.max(4, slotWidth * 0.62)));
	let maximum = $derived.by(() => niceMaximum(points.reduce(
		(current, point) => Math.max(current, metricValue(point)),
		0,
	)));
	let labelInterval = $derived(Math.max(1, Math.ceil(points.length / 6)));
	let ticks = $derived(Array.from({ length: 5 }, (_, index) => maximum * (4 - index) / 4));

	$effect(() => {
		if (points.length === 0) activeIndex = 0;
		else if (activeIndex >= points.length) activeIndex = points.length - 1;
		if (focusedIndex !== null && focusedIndex >= points.length) focusedIndex = null;
		if (tooltip !== null && tooltip.index >= points.length) hideTooltip();
	});

	function niceMaximum(value: number): number {
		if (value <= 0) return 1;
		const magnitude = 10 ** Math.floor(Math.log10(value));
		const normalized = value / magnitude;
		const rounded = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
		return rounded * magnitude;
	}

	function metricValue(point: UsageTrendPoint): number {
		if (metric === "cost") return costValue(point);
		return point.summary.fresh_input_tokens + point.summary.cached_input_tokens + point.summary.output_tokens;
	}

	function costValue(point: UsageTrendPoint): number {
		const value = Number(point.summary.known_estimated_cost_usd);
		return Number.isFinite(value) ? Math.max(0, value) : 0;
	}

	function segmentHeight(value: number): number {
		return Math.max(0, value / maximum * plotHeight);
	}

	function slotX(index: number): number {
		return margin.left + index * slotWidth;
	}

	function formatAxis(value: number): string {
		if (metric === "tokens") return formatCompact(value);
		return new Intl.NumberFormat(currentDesktopLocale(), {
			style: "currency",
			currency: "USD",
			minimumFractionDigits: 0,
			maximumFractionDigits: value > 0 && value < 0.01 ? 6 : 2,
		}).format(value);
	}

	function formatCompact(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale(), {
			notation: "compact",
			maximumFractionDigits: 1,
		}).format(value);
	}

	function formatInteger(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale()).format(value);
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

	function formatPercent(value: number): string {
		return new Intl.NumberFormat(currentDesktopLocale(), {
			style: "percent",
			maximumFractionDigits: 1,
		}).format(value);
	}

	function bucketLabel(unixMS: number): string {
		const value = new Date(unixMS);
		const locale = currentDesktopLocale();
		const timeZone = reportTimeZone();
		switch (range.bucket_unit) {
			case "hour":
				return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit", timeZoneName: "short", timeZone }).format(value);
			case "month":
				return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", timeZone }).format(value);
			case "year":
				return new Intl.DateTimeFormat(locale, { year: "numeric", timeZone }).format(value);
			default:
				return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", timeZone }).format(value);
		}
	}

	function fullBucketLabel(unixMS: number): string {
		const value = new Date(unixMS);
		const locale = currentDesktopLocale();
		const timeZone = reportTimeZone();
		if (range.bucket_unit === "hour") {
			return new Intl.DateTimeFormat(locale, {
				year: "numeric",
				month: "short",
				day: "numeric",
				hour: "2-digit",
				minute: "2-digit",
				timeZoneName: "short",
				timeZone,
			}).format(value);
		}
		if (range.bucket_unit === "month") {
			return new Intl.DateTimeFormat(locale, { year: "numeric", month: "long", timeZone }).format(value);
		}
		if (range.bucket_unit === "year") {
			return new Intl.DateTimeFormat(locale, { year: "numeric", timeZone }).format(value);
		}
		return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeZone }).format(value);
	}

	function reportTimeZone(): string | undefined {
		const value = range.time_zone;
		if (!value || value === "Local") return undefined;
		try {
			new Intl.DateTimeFormat("en-US", { timeZone: value }).format(0);
			return value;
		} catch {
			return undefined;
		}
	}

	function bucketAriaLabel(point: UsageTrendPoint): string {
		if (metric === "cost") {
			if (point.summary.partial_cost_event_count > 0) {
				return translate("usage.chart.partialCostBucketAria", {
					bucket: fullBucketLabel(point.start_unix_ms),
					cost: formatCurrency(point.summary.known_estimated_cost_usd),
					coverage: formatPercent(point.summary.pricing_coverage),
				});
			}
			return translate("usage.chart.costBucketAria", {
				bucket: fullBucketLabel(point.start_unix_ms),
				cost: formatCurrency(point.summary.known_estimated_cost_usd),
				coverage: formatPercent(point.summary.pricing_coverage),
			});
		}
		return translate("usage.chart.tokenBucketAria", {
			bucket: fullBucketLabel(point.start_unix_ms),
			fresh: formatInteger(point.summary.fresh_input_tokens),
			cached: formatInteger(point.summary.cached_input_tokens),
			output: formatInteger(point.summary.output_tokens),
			total: formatInteger(point.summary.total_tokens),
		});
	}

	async function placeTooltip(index: number, left: number, top: number, source: TooltipSource) {
		const revision = ++tooltipRevision;
		activeIndex = index;
		tooltip = { index, left, top, source };
		await tick();
		if (revision !== tooltipRevision || tooltip === null || !containerElement || !tooltipElement) return;

		const padding = 8;
		const offset = 12;
		const containerRect = containerElement.getBoundingClientRect();
		let nextLeft = left + offset;
		let nextTop = top + offset;
		if (nextLeft + tooltipElement.offsetWidth > containerRect.width - padding) nextLeft = left - tooltipElement.offsetWidth - offset;
		if (nextTop + tooltipElement.offsetHeight > containerRect.height - padding) nextTop = top - tooltipElement.offsetHeight - offset;
		nextLeft = Math.max(padding, Math.min(nextLeft, containerRect.width - tooltipElement.offsetWidth - padding));
		nextTop = Math.max(padding, Math.min(nextTop, containerRect.height - tooltipElement.offsetHeight - padding));
		tooltip = { index, left: nextLeft, top: nextTop, source };
	}

	function showPointerTooltip(event: PointerEvent, index: number) {
		if (!containerElement) return;
		const rect = containerElement.getBoundingClientRect();
		void placeTooltip(index, event.clientX - rect.left, event.clientY - rect.top, "pointer");
	}

	function showFocusTooltip(element: SVGGElement, index: number) {
		if (!containerElement) return;
		const containerRect = containerElement.getBoundingClientRect();
		const bucketRect = element.getBoundingClientRect();
		void placeTooltip(index, bucketRect.left - containerRect.left + bucketRect.width / 2, bucketRect.top - containerRect.top, "focus");
	}

	function restoreFocusedTooltip() {
		if (focusedIndex === null || !containerElement) {
			hideTooltip();
			return;
		}
		const elements = bucketElements();
		const element = elements[focusedIndex];
		if (element) showFocusTooltip(element, focusedIndex);
	}

	function hideTooltip() {
		tooltipRevision++;
		tooltip = null;
	}

	function bucketElements(): SVGGElement[] {
		return containerElement ? Array.from(containerElement.querySelectorAll<SVGGElement>("[data-usage-bucket]")) : [];
	}

	function focusBucket(index: number) {
		const elements = bucketElements();
		const next = elements[index];
		if (!next) return;
		activeIndex = index;
		next.focus();
	}

	function handleBucketKeydown(event: KeyboardEvent, index: number) {
		let nextIndex = index;
		switch (event.key) {
			case "ArrowLeft":
				nextIndex = Math.max(0, index - 1);
				break;
			case "ArrowRight":
				nextIndex = Math.min(points.length - 1, index + 1);
				break;
			case "Home":
				nextIndex = 0;
				break;
			case "End":
				nextIndex = Math.max(0, points.length - 1);
				break;
			case "Enter":
			case " ":
				event.preventDefault();
				showFocusTooltip(event.currentTarget as SVGGElement, index);
				return;
			default:
				return;
		}
		event.preventDefault();
		focusBucket(nextIndex);
	}
</script>

<div class="relative flex flex-col gap-3" bind:this={containerElement}>
	{#if metric === "tokens"}
		<div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-hidden="true">
			<span class="inline-flex items-center gap-1.5"><span class="size-2.5 rounded-sm bg-chart-1"></span>{$_("usage.freshInput")}</span>
			<span class="inline-flex items-center gap-1.5"><span class="size-2.5 rounded-sm bg-chart-2"></span>{$_("usage.cachedInput")}</span>
			<span class="inline-flex items-center gap-1.5"><span class="size-2.5 rounded-sm bg-chart-3"></span>{$_("usage.output")}</span>
		</div>
	{/if}

	<svg class="h-auto w-full overflow-visible" viewBox={`0 0 ${width} ${height}`} role="group" aria-labelledby={`${chartTitleID} ${chartDescriptionID}`}>
		<title id={chartTitleID}>{metric === "cost" ? $_("usage.chart.costTitle") : $_("usage.chart.tokenTitle")}</title>
		<desc id={chartDescriptionID}>{metric === "cost" ? $_("usage.chart.costDescription") : $_("usage.chart.tokenDescription")}</desc>

		{#each ticks as tick, index (index)}
			{@const y = margin.top + index * plotHeight / 4}
			<line x1={margin.left} x2={width - margin.right} y1={y} y2={y} stroke="var(--border)" stroke-width="1" />
			<text x={margin.left - 8} y={y + 4} text-anchor="end" fill="var(--muted-foreground)" font-size="10">{formatAxis(tick)}</text>
		{/each}

		{#each points as point, index (point.start_unix_ms)}
			{@const freshHeight = segmentHeight(point.summary.fresh_input_tokens)}
			{@const cachedHeight = segmentHeight(point.summary.cached_input_tokens)}
			{@const outputHeight = segmentHeight(point.summary.output_tokens)}
			{@const costHeight = segmentHeight(costValue(point))}
			<g
				data-usage-bucket
				transform={`translate(${slotX(index)} 0)`}
				role="button"
				tabindex={activeIndex === index ? 0 : -1}
				aria-label={bucketAriaLabel(point)}
				aria-describedby={tooltip?.source === "focus" && tooltip.index === index ? tooltipID : undefined}
				onpointerenter={(event) => showPointerTooltip(event, index)}
				onpointermove={(event) => showPointerTooltip(event, index)}
				onpointerleave={restoreFocusedTooltip}
				onfocus={(event) => {
					focusedIndex = index;
					showFocusTooltip(event.currentTarget as SVGGElement, index);
				}}
				onblur={() => {
					focusedIndex = null;
					if (tooltip?.source === "focus") hideTooltip();
				}}
				onclick={(event) => (event.currentTarget as SVGGElement).focus()}
				onkeydown={(event) => handleBucketKeydown(event, index)}
				class="cursor-pointer outline-none focus-visible:[&_rect[data-focus]]:stroke-ring"
			>
				<rect data-focus x="1" y={margin.top} width={Math.max(1, slotWidth - 2)} height={plotHeight} rx="3" fill={tooltip?.index === index ? "var(--accent)" : "transparent"} fill-opacity={tooltip?.index === index ? 0.65 : 0} stroke="transparent" stroke-width="2" />
				{#if metric === "cost"}
					<rect x={(slotWidth - barWidth) / 2} y={plotBottom - costHeight} width={barWidth} height={costHeight} fill="var(--chart-1)" />
				{:else}
					<rect x={(slotWidth - barWidth) / 2} y={plotBottom - freshHeight} width={barWidth} height={freshHeight} fill="var(--chart-1)" />
					<rect x={(slotWidth - barWidth) / 2} y={plotBottom - freshHeight - cachedHeight} width={barWidth} height={cachedHeight} fill="var(--chart-2)" />
					<rect x={(slotWidth - barWidth) / 2} y={plotBottom - freshHeight - cachedHeight - outputHeight} width={barWidth} height={outputHeight} fill="var(--chart-3)" />
				{/if}
			</g>
			{#if index % labelInterval === 0 || index === points.length - 1}
				<text x={slotX(index) + slotWidth / 2} y={height - 13} text-anchor="middle" fill="var(--muted-foreground)" font-size="10">{bucketLabel(point.start_unix_ms)}</text>
			{/if}
		{/each}
	</svg>

	{#if tooltip && tooltipPoint}
		<div
			id={tooltipID}
			role="tooltip"
			bind:this={tooltipElement}
			class="pointer-events-none absolute min-w-44 max-w-56 rounded-md border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
			style={`left: ${tooltip.left}px; top: ${tooltip.top}px;`}
		>
			<div class="font-medium">{fullBucketLabel(tooltipPoint.start_unix_ms)}</div>
			{#if metric === "cost"}
				<div class="mt-1 flex items-center justify-between gap-4"><span>{$_("usage.chart.knownCost")}</span><span class="font-mono tabular-nums">{formatCurrency(tooltipPoint.summary.known_estimated_cost_usd)}</span></div>
				{#if tooltipPoint.summary.partial_cost_event_count > 0}
					<div class="mt-1 text-muted-foreground">{$_("usage.chart.partialEstimate")}</div>
				{/if}
				{#if tooltipPoint.summary.event_count > 0 && tooltipPoint.summary.pricing_coverage < 1}
					<div class="mt-1 flex items-center justify-between gap-4 text-muted-foreground"><span>{$_("usage.chart.coverage")}</span><span class="font-mono tabular-nums">{formatPercent(tooltipPoint.summary.pricing_coverage)}</span></div>
				{/if}
			{:else}
				<div class="mt-1 flex items-center justify-between gap-4"><span>{$_("usage.total")}</span><span class="font-mono tabular-nums">{formatInteger(tooltipPoint.summary.total_tokens)}</span></div>
				<div class="mt-1 grid grid-cols-2 gap-x-4 text-muted-foreground">
					<span>{$_("usage.freshInput")}</span><span class="text-right font-mono tabular-nums">{formatInteger(tooltipPoint.summary.fresh_input_tokens)}</span>
					<span>{$_("usage.cachedInput")}</span><span class="text-right font-mono tabular-nums">{formatInteger(tooltipPoint.summary.cached_input_tokens)}</span>
					<span>{$_("usage.output")}</span><span class="text-right font-mono tabular-nums">{formatInteger(tooltipPoint.summary.output_tokens)}</span>
				</div>
			{/if}
		</div>
	{/if}
</div>
