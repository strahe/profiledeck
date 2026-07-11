<script lang="ts">
	import type { Snippet } from "svelte";

	import * as Card from "$lib/components/ui/card";
	import InfoTooltip from "./InfoTooltip.svelte";

	let {
		title,
		help = "",
		description = "",
		action,
		children,
		class: className = "",
		contentClass = "",
	}: {
		title: string;
		help?: string;
		description?: string;
		action?: Snippet;
		children?: Snippet;
		class?: string;
		contentClass?: string;
	} = $props();
</script>

<Card.Root class={className}>
	<Card.Header>
		<Card.Title class="flex items-center gap-1">
			<span>{title}</span>
			{#if help}<InfoTooltip content={help} subject={title} />{/if}
		</Card.Title>
		{#if description}<Card.Description>{description}</Card.Description>{/if}
		{#if action}<Card.Action>{@render action()}</Card.Action>{/if}
	</Card.Header>
	<Card.Content class={contentClass}>{@render children?.()}</Card.Content>
</Card.Root>
