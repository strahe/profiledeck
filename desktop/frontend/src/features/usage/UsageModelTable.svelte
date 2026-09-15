<script lang="ts">
	import { _ } from "svelte-i18n";
	import type { UsageModelSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/usage/models";

	import { Badge } from "$lib/components/ui/badge";
	import * as Table from "$lib/components/ui/table";
	import { currentDesktopLocale } from "$lib/i18n";
	import { cn } from "$lib/utils";

	let { models, showReportedCost = false }: { models: UsageModelSummary[]; showReportedCost?: boolean } = $props();

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

	function formatReportedCurrency(value: string): string {
		const parsed = Number(value);
		if (!Number.isFinite(parsed)) return value;
		return new Intl.NumberFormat(currentDesktopLocale(), {
			style: "currency",
			currency: "USD",
			minimumFractionDigits: 2,
			maximumFractionDigits: 10,
		}).format(parsed);
	}
</script>

<div class="overflow-x-auto">
	<Table.Root class={cn(showReportedCost ? "min-w-[680px]" : "min-w-[520px]")}>
		<Table.Header>
			<Table.Row>
				<Table.Head>{$_("usage.models.model")}</Table.Head>
				<Table.Head class="text-right">{$_("usage.models.apiEquivalentCost")}</Table.Head>
				{#if showReportedCost}<Table.Head class="text-right">{$_("usage.models.reportedCost")}</Table.Head>{/if}
				<Table.Head class="text-right">{$_("usage.models.tokens")}</Table.Head>
				<Table.Head class="text-right">{$_("usage.models.sessions")}</Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each models as model (model.model)}
				<Table.Row>
					<Table.Cell class="font-mono text-xs font-medium">{model.model}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">
						<div class="flex items-center justify-end gap-2">
							<span>{formatCurrency(model.summary.known_estimated_cost_usd)}</span>
							{#if model.summary.cost_status !== "estimated"}<Badge variant="outline">{model.summary.pricing_coverage > 0 ? $_("usage.pricing.partial") : $_("usage.pricing.unknown")}</Badge>{/if}
						</div>
					</Table.Cell>
					{#if showReportedCost}
						<Table.Cell class="text-right tabular-nums">
							<div class="flex items-center justify-end gap-2">
								<span>{model.summary.reported_cost_event_count + model.summary.partial_reported_cost_event_count > 0 ? formatReportedCurrency(model.summary.known_reported_cost_usd) : "—"}</span>
								{#if model.summary.reported_cost_status !== "reported"}<Badge variant="outline">{model.summary.reported_cost_event_count + model.summary.partial_reported_cost_event_count > 0 ? $_("usage.reportedCostStatus.partial") : $_("usage.reportedCostStatus.unavailable")}</Badge>{/if}
							</div>
						</Table.Cell>
					{/if}
					<Table.Cell class="text-right tabular-nums">{formatInteger(model.summary.total_tokens)}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{formatInteger(model.summary.session_count)}</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>
