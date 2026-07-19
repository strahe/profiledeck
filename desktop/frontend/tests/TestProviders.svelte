<script lang="ts">
	import type { Snippet } from "svelte";

	import * as Tooltip from "$lib/components/ui/tooltip";

	let { children }: { children: Snippet } = $props();
</script>

<Tooltip.Provider>
	{@render children()}
</Tooltip.Provider>
