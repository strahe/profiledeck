<script lang="ts">
	import { _ } from "svelte-i18n";
	import type { UsageModelSummary } from "../../../bindings/github.com/strahe/profiledeck/internal/usage/models";

	import { Badge } from "$lib/components/ui/badge";
	import * as Table from "$lib/components/ui/table";
	import { currentDesktopLocale } from "$lib/i18n";

	let { models }: { models: UsageModelSummary[] } = $props();

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
</script>

<div class="overflow-x-auto">
	<Table.Root class="min-w-[520px]">
		<Table.Header>
			<Table.Row>
				<Table.Head>{$_("usage.models.model")}</Table.Head>
				<Table.Head class="text-right">{$_("usage.models.knownCost")}</Table.Head>
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
					<Table.Cell class="text-right tabular-nums">{formatInteger(model.summary.total_tokens)}</Table.Cell>
					<Table.Cell class="text-right tabular-nums">{formatInteger(model.summary.session_count)}</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
</div>
