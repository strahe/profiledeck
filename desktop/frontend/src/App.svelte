<script lang="ts">
	import Router from "svelte-spa-router";
	import { ModeWatcher } from "mode-watcher";

	import { Toaster } from "$lib/components/ui/sonner";
	import * as Tooltip from "$lib/components/ui/tooltip";
	import Workspace from "./features/workspace/Workspace.svelte";

	const routes = {
		"*": Workspace,
	};
</script>

<ModeWatcher defaultMode="system" modeStorageKey="profiledeck-appearance" />
<Tooltip.Provider delayDuration={250}>
	<Router {routes} />
	<Toaster richColors position="top-right" />
</Tooltip.Provider>
