<script lang="ts">
	import Router from "svelte-spa-router";
	import { wrap } from "svelte-spa-router/wrap";

	import { Toaster } from "$lib/components/ui/sonner";
	import Workspace from "./features/workspace/Workspace.svelte";

	const routes = {
		"/": wrap({ component: Workspace, props: { initialView: "profiles" } }),
		"/codex/profiles": wrap({ component: Workspace, props: { initialView: "profiles" } }),
		"/codex/profiles/new": wrap({ component: Workspace, props: { initialView: "profiles", profileRoute: "new" } }),
		"/codex/profiles/:id": wrap({ component: Workspace, props: { initialView: "profiles", profileRoute: "detail" } }),
		"/codex/profiles/:id/fork": wrap({ component: Workspace, props: { initialView: "profiles", profileRoute: "fork" } }),
		"/codex/health": wrap({ component: Workspace, props: { initialView: "health" } }),
		"/codex/usage": wrap({ component: Workspace, props: { initialView: "usage" } }),
		"/settings": wrap({ component: Workspace, props: { initialView: "settings" } }),
		"*": wrap({ component: Workspace, props: { initialView: "profiles" } }),
	};
</script>

<Toaster richColors position="top-right" />
<Router {routes} />
