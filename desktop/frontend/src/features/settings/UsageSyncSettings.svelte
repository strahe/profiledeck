<script lang="ts">
	import { _ } from "svelte-i18n";

	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import * as Select from "$lib/components/ui/select";
	import { Spinner } from "$lib/components/ui/spinner";

	import { usageIntervals } from "./usage-sync-settings";

	let {
		id,
		interval,
		loading = false,
		busy = false,
		onChange,
	}: {
		id: string;
		interval: number;
		loading?: boolean;
		busy?: boolean;
		onChange: (value: string) => void;
	} = $props();
</script>

<SectionCard title={$_("usageSettings.title")} description={$_("usageSettings.description")}>
	<SettingsRow label={$_("usageSettings.label")} forID={id}>
		{#snippet control()}
			{#if busy}<Spinner />{/if}
			<Select.Root type="single" value={String(interval)} onValueChange={onChange}>
				<Select.Trigger {id} class="min-w-32" disabled={loading || busy}>
					{$_("usageSettings.seconds", { values: { count: interval } })}
				</Select.Trigger>
				<Select.Content>
					<Select.Group>
						{#each usageIntervals as seconds (seconds)}
							<Select.Item
								value={String(seconds)}
								label={$_("usageSettings.seconds", { values: { count: seconds } })}
							/>
						{/each}
					</Select.Group>
				</Select.Content>
			</Select.Root>
		{/snippet}
	</SettingsRow>
</SectionCard>
