<script lang="ts">
	import FolderIcon from "@lucide/svelte/icons/folder";
	import * as Empty from "$lib/components/ui/empty";

	let { title, description }: { title: string; description: string } = $props();
</script>

<Empty.Root>
	<Empty.Header>
		<Empty.Media variant="icon"><FolderIcon /></Empty.Media>
		<Empty.Title>{title}</Empty.Title>
		<Empty.Description>{description}</Empty.Description>
	</Empty.Header>
</Empty.Root>
