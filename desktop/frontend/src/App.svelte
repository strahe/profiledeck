<script lang="ts">
	import Router from "svelte-spa-router";

	import { Toaster } from "$lib/components/ui/sonner";
	import Workspace from "./features/workspace/Workspace.svelte";

	const routes = {
		"*": Workspace,
	};
</script>

<Toaster richColors position="top-right" />
<Router {routes} />
