<script lang="ts">
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";
	import { _ } from "svelte-i18n";

	import * as Alert from "$lib/components/ui/alert";
	import { Spinner } from "$lib/components/ui/spinner";

	let { state }: { state: "loading" | "error" } = $props();
</script>

<div class="grid h-full min-h-48 place-items-center">
	{#if state === "loading"}
		<Spinner aria-label={$_("workspaceView.loadingPage")} />
	{:else}
		<Alert.Root variant="destructive" class="max-w-lg">
			<TriangleAlertIcon data-icon="inline-start" />
			<Alert.Title>{$_("workspaceView.unavailableTitle")}</Alert.Title>
			<Alert.Description>{$_("workspaceView.unavailableDescription")}</Alert.Description>
		</Alert.Root>
	{/if}
</div>
