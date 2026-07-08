<script lang="ts">
	import * as Card from "$lib/components/ui/card";

	let { label, value }: { label: string; value: string | number } = $props();
</script>

<Card.Root>
	<Card.Header class="py-3">
		<Card.Description>{label}</Card.Description>
		<Card.Title class="text-xl">{value}</Card.Title>
	</Card.Header>
</Card.Root>
