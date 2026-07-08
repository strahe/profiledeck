<script lang="ts">
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { buttonVariants } from "$lib/components/ui/button";

	let {
		label,
		title,
		description,
		disabled = false,
		onConfirm,
	}: {
		label: string;
		title: string;
		description: string;
		disabled?: boolean;
		onConfirm: () => void | Promise<void>;
	} = $props();
</script>

<AlertDialog.Root>
	<AlertDialog.Trigger {disabled} class={buttonVariants({ variant: "outline", size: "sm" })}>
		{label}
	</AlertDialog.Trigger>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{title}</AlertDialog.Title>
			<AlertDialog.Description>{description}</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>Cancel</AlertDialog.Cancel>
			<AlertDialog.Action onclick={onConfirm}>Confirm</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
