<script lang="ts">
	import type { Snippet } from "svelte";

	import * as Tooltip from "$lib/components/ui/tooltip";
	import { Button } from "$lib/components/ui/button";

	let {
		label,
		disabled = false,
		variant = "outline",
		onclick,
		children,
	}: {
		label: string;
		disabled?: boolean;
		variant?: "default" | "outline" | "ghost" | "secondary" | "destructive";
		onclick?: (event: MouseEvent) => void;
		children?: Snippet;
	} = $props();
</script>

<Tooltip.Root>
	<Tooltip.Trigger>
		{#snippet child({ props })}
			<Button {...props} type="button" size="icon-sm" {variant} {disabled} {onclick} aria-label={label}>
				{@render children?.()}
			</Button>
		{/snippet}
	</Tooltip.Trigger>
	<Tooltip.Content>{label}</Tooltip.Content>
</Tooltip.Root>
