<script lang="ts">
	import type { Snippet } from "svelte";
	import InfoTooltip from "./InfoTooltip.svelte";

	let {
		title,
		help = "",
		description = "",
		breadcrumbs,
		status,
		meta,
		actions,
	}: {
		title: string;
		help?: string;
		description?: string;
		breadcrumbs?: Snippet;
		status?: Snippet;
		meta?: Snippet;
		actions?: Snippet;
	} = $props();
</script>

<header class="flex flex-col gap-3">
	{@render breadcrumbs?.()}
	<div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
		<div class="flex min-w-0 flex-col gap-1">
			<div class="flex min-w-0 flex-wrap items-center gap-2">
				<h2 class="truncate text-xl font-semibold tracking-tight">{title}</h2>
				{#if help}<InfoTooltip content={help} subject={title} />{/if}
				{@render status?.()}
			</div>
			{#if description}<p class="max-w-3xl text-sm text-muted-foreground">{description}</p>{/if}
			{@render meta?.()}
		</div>
		{#if actions}<div class="flex shrink-0 items-center gap-2">{@render actions()}</div>{/if}
	</div>
</header>
