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
		control: Snippet;
	} = $props();
</script>

<Field.Field orientation="horizontal" data-disabled={disabled || undefined}>
	<Field.FieldContent class="gap-1">
		<Field.FieldLabel for={forID}>{label}</Field.FieldLabel>
		{#if description}<Field.FieldDescription size="compact">{description}</Field.FieldDescription>{/if}
		{#if message}<Field.FieldDescription size="compact">{message}</Field.FieldDescription>{/if}
	</Field.FieldContent>
	<div class="flex shrink-0 items-center justify-end gap-2">{@render control()}</div>
</Field.Field>
