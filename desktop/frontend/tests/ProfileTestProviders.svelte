<script lang="ts">
	import type { Snippet } from "svelte";

	import * as Tooltip from "$lib/components/ui/tooltip";
	import { provideCodexRuntime } from "../src/features/settings/codex-runtime.svelte.js";

	let { children }: { children: Snippet } = $props();

	provideCodexRuntime({
		showError: () => {},
		showNotice: () => {},
	});
</script>

<Tooltip.Provider>
	{@render children()}
</Tooltip.Provider>
