<script lang="ts">
	import type { Snippet } from "svelte";

	import * as Field from "$lib/components/ui/field";
	import InfoTooltip from "./InfoTooltip.svelte";

	let {
		label,
		help = "",
		description = "",
		message = "",
		forID,
		disabled = false,
		control,
	}: {
		label: string;
		help?: string;
		description?: string;
		message?: string;
		forID?: string;
		disabled?: boolean;
		control: Snippet;
	} = $props();
</script>

<Field.Field orientation="horizontal" data-disabled={disabled || undefined}>
	<Field.FieldContent>
		<div class="flex items-center gap-1">
			<Field.FieldLabel for={forID}>{label}</Field.FieldLabel>
			{#if help}<InfoTooltip content={help} subject={label} />{/if}
		</div>
		{#if description}<Field.FieldDescription>{description}</Field.FieldDescription>{/if}
		{#if message}<Field.FieldDescription>{message}</Field.FieldDescription>{/if}
	</Field.FieldContent>
	<div class="flex shrink-0 items-center justify-end gap-2">{@render control()}</div>
</Field.Field>
