<script lang="ts">
	import { Badge } from "$lib/components/ui/badge";
	import * as Table from "$lib/components/ui/table";
	import ConfirmAction from "./ConfirmAction.svelte";
	import EmptyState from "./EmptyState.svelte";
	import type { DoctorOperation } from "../../../../bindings/github.com/strahe/profiledeck/internal/app/models";

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
		if (level === "ERROR" || level === "failed") return "destructive";
		if (level === "WARN" || level === "pending") return "secondary";
		return "outline";
	}
</script>

{#if operations.length}
	<Table.Root>
		<Table.Header>
			<Table.Row>
				<Table.Head>ID</Table.Head>
				<Table.Head>Status</Table.Head>
				<Table.Head>Profile</Table.Head>
				<Table.Head>Recovery</Table.Head>
				<Table.Head></Table.Head>
			</Table.Row>
		</Table.Header>
		<Table.Body>
			{#each operations as operation}
				<Table.Row>
					<Table.Cell class="font-mono text-xs">{operation.id}</Table.Cell>
					<Table.Cell><Badge variant={statusBadgeVariant(operation.level)}>{operation.status}</Badge></Table.Cell>
					<Table.Cell>{operation.profile_id || "-"}</Table.Cell>
					<Table.Cell>{operation.recovery_status || operation.reason || "-"}</Table.Cell>
					<Table.Cell class="text-right">
						{#if operation.recovery_status === "recoverable"}
							<ConfirmAction
								label="Recover"
								title="Recover failed switch?"
								description="This restores target files through the recovery pipeline."
								disabled={!!actionBusy}
								onConfirm={() => onRecover(operation.id)}
							/>
						{/if}
					</Table.Cell>
				</Table.Row>
			{/each}
		</Table.Body>
	</Table.Root>
{:else}
	<EmptyState title="No incomplete operations" description="No pending or failed operations are currently reported." />
{/if}
