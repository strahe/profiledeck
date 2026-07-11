<script lang="ts">
	import { _ } from "svelte-i18n";
	import ClipboardIcon from "@lucide/svelte/icons/clipboard";

	import type { DoctorOperation } from "../../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import IconAction from "./IconAction.svelte";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { Badge } from "$lib/components/ui/badge";
	import { buttonVariants } from "$lib/components/ui/button";
	import * as Table from "$lib/components/ui/table";

	let {
		operations,
		actionBusy = "",
		onRecover,
	}: {
		operations: DoctorOperation[];
		actionBusy?: string;
		onRecover: (operationID: string) => void | Promise<void>;
	} = $props();

	function statusBadgeVariant(level: string | undefined): "default" | "secondary" | "destructive" | "outline" {
		const normalized = level?.toLowerCase();
		if (normalized === "error" || normalized === "failed") return "destructive";
		if (normalized === "warn" || normalized === "pending") return "secondary";
		return "outline";
	}

	function shortID(value: string): string {
		return value.length > 8 ? `…${value.slice(-8)}` : value;
	}

	function copyID(value: string) {
		void navigator.clipboard?.writeText(value);
	}
</script>

<Table.Root>
	<Table.Header>
		<Table.Row>
			<Table.Head>{$_("diagnosticsPage.operation")}</Table.Head>
			<Table.Head>{$_("diagnosticsPage.status")}</Table.Head>
			<Table.Head>{$_("diagnosticsPage.profile")}</Table.Head>
			<Table.Head>{$_("diagnosticsPage.message")}</Table.Head>
			<Table.Head><span class="sr-only">{$_("actions.actions")}</span></Table.Head>
		</Table.Row>
	</Table.Header>
	<Table.Body>
		{#each operations as operation (operation.id)}
			<Table.Row>
				<Table.Cell>
					<div class="flex items-center gap-1">
						<span class="font-mono text-xs">{shortID(operation.id)}</span>
						<IconAction label={$_("actions.copyOperationID")} variant="ghost" onclick={() => copyID(operation.id)}><ClipboardIcon /></IconAction>
					</div>
				</Table.Cell>
				<Table.Cell><Badge variant={statusBadgeVariant(operation.level)}>{operation.status}</Badge></Table.Cell>
				<Table.Cell class="font-mono text-xs">{operation.profile_id ? shortID(operation.profile_id) : "—"}</Table.Cell>
				<Table.Cell class="max-w-sm text-sm text-muted-foreground">{operation.recovery_reason || operation.reason || operation.error_message || "—"}</Table.Cell>
				<Table.Cell class="text-right">
					{#if operation.recovery_status === "recoverable"}
						<AlertDialog.Root>
							<AlertDialog.Trigger disabled={!!actionBusy} class={buttonVariants({ variant: "outline", size: "sm" })}>
								{$_("actions.recover")}
							</AlertDialog.Trigger>
							<AlertDialog.Content>
								<AlertDialog.Header>
									<AlertDialog.Title>{$_("diagnosticsPage.recover.title")}</AlertDialog.Title>
									<AlertDialog.Description>{$_("diagnosticsPage.recover.description")}</AlertDialog.Description>
								</AlertDialog.Header>
								<AlertDialog.Footer>
									<AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel>
									<AlertDialog.Action onclick={() => onRecover(operation.id)}>{$_("actions.recover")}</AlertDialog.Action>
								</AlertDialog.Footer>
							</AlertDialog.Content>
						</AlertDialog.Root>
					{/if}
				</Table.Cell>
			</Table.Row>
		{/each}
	</Table.Body>
</Table.Root>
