<script lang="ts">
	import type { Snippet } from "svelte";

	import * as Field from "$lib/components/ui/field";

	let {
		label,
		description = "",
		message = "",
		forID,
		disabled = false,
		control,
	}: {
		label: string;
		description?: string;
		message?: string;
		forID?: string;
		disabled?: boolean;
		control?: Snippet;
	} = $props();
</script>

<Field.Field orientation="horizontal" data-disabled={disabled || undefined}>
	<Field.FieldContent class="gap-1">
		<Field.FieldLabel for={forID}>{label}</Field.FieldLabel>
		{#if description}<Field.FieldDescription class="text-xs leading-relaxed">{description}</Field.FieldDescription>{/if}
		{#if message}<Field.FieldDescription class="text-xs leading-relaxed">{message}</Field.FieldDescription>{/if}
	</Field.FieldContent>
	{#if control}
		<div class="flex shrink-0 items-center justify-end gap-2">{@render control()}</div>
	{/if}
</Field.Field>
