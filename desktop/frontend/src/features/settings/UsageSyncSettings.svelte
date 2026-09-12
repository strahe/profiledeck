<script lang="ts">
	import { _ } from "svelte-i18n";

	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import * as Select from "$lib/components/ui/select";
	import { Spinner } from "$lib/components/ui/spinner";

	import { usageIntervalLabel, usageIntervals } from "./usage-sync-settings";

	let {
		id,
		interval,
		description,
		loading = false,
		busy = false,
		onChange,
	}: {
		id: string;
		interval: number;
		description?: string;
		loading?: boolean;
		busy?: boolean;
		onChange: (value: string) => void;
	} = $props();

	const selected = $derived(usageIntervalLabel(interval));
</script>

<SectionCard title={$_("usageSettings.title")} description={description ?? $_("usageSettings.description")}>
	<SettingsRow label={$_("usageSettings.label")} forID={id}>
		{#snippet control()}
			{#if busy}<Spinner />{/if}
			<Select.Root type="single" value={String(interval)} onValueChange={onChange}>
				<Select.Trigger {id} class="min-w-36" disabled={loading || busy}>
					{$_(selected.key, { values: { count: selected.count } })}
				</Select.Trigger>
				<Select.Content>
					<Select.Group>
						{#each usageIntervals as seconds (seconds)}
							{@const option = usageIntervalLabel(seconds)}
							<Select.Item
								value={String(seconds)}
								label={$_(option.key, { values: { count: option.count } })}
							/>
						{/each}
					</Select.Group>
				</Select.Content>
			</Select.Root>
		{/snippet}
	</SettingsRow>
</SectionCard>
