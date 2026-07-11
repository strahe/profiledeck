<script lang="ts">
	import { _ } from "svelte-i18n";
	import InfoIcon from "@lucide/svelte/icons/info";

	import { Button } from "$lib/components/ui/button";
	import * as Tooltip from "$lib/components/ui/tooltip";

	let {
		content,
		subject = "",
	}: {
		content: string;
		subject?: string;
	} = $props();
</script>

<Tooltip.Root>
	<Tooltip.Trigger>
		{#snippet child({ props })}
			<Button
				{...props}
				type="button"
				variant="ghost"
				size="icon-xs"
				aria-label={subject
					? $_("actions.moreInformationAbout", { values: { subject } })
					: $_("actions.moreInformation")}
			>
				<InfoIcon />
			</Button>
		{/snippet}
	</Tooltip.Trigger>
	<Tooltip.Content class="max-w-72">{content}</Tooltip.Content>
</Tooltip.Root>
