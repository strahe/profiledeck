<script lang="ts">
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import FileLockIcon from "@lucide/svelte/icons/file-lock";

	import * as Alert from "$lib/components/ui/alert";
	import * as Dialog from "$lib/components/ui/dialog";
	import * as Table from "$lib/components/ui/table";
	import { Badge, type BadgeVariant } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Spinner } from "$lib/components/ui/spinner";

	import type { CodexProfileImportPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	interface Props {
		open?: boolean;
		plan: CodexProfileImportPlan | null;
		busy: boolean;
		onClose: () => void;
		onApply: () => void;
	}

	let { open = $bindable(false), plan, busy, onClose, onApply }: Props = $props();

	function actionVariant(action: string): BadgeVariant {
		if (action === "conflict") return "destructive";
		if (action === "unchanged") return "outline";
		return "secondary";
	}
</script>

<Dialog.Root {open} onOpenChange={(value) => { open = value; if (!value) onClose(); }}>
	<Dialog.Content class="sm:max-w-3xl">
		<Dialog.Header>
			<Dialog.Title>{$_("profileTransfer.import.title")}</Dialog.Title>
			<Dialog.Description>{$_("profileTransfer.import.description")}</Dialog.Description>
		</Dialog.Header>

		{#if plan}
			<div class="flex flex-col gap-4">
				<Alert.Root>
					<FileLockIcon data-icon="inline-start" />
					<Alert.Title>{$_("profileTransfer.sensitive.title")}</Alert.Title>
					<Alert.Description>{$_("profileTransfer.sensitive.description")}</Alert.Description>
				</Alert.Root>

				<div class="flex flex-wrap items-center gap-2">
					<Badge variant="secondary">{$_("profileTransfer.counts.create", { values: { count: plan.counts.create } })}</Badge>
					<Badge variant="outline">{$_("profileTransfer.counts.unchanged", { values: { count: plan.counts.unchanged } })}</Badge>
					<Badge variant={plan.counts.conflict ? "destructive" : "outline"}>{$_("profileTransfer.counts.conflict", { values: { count: plan.counts.conflict } })}</Badge>
				</div>

				{#if plan.counts.conflict}
					<Alert.Root variant="destructive">
						<AlertTriangleIcon data-icon="inline-start" />
						<Alert.Title>{$_("profileTransfer.import.conflictTitle")}</Alert.Title>
						<Alert.Description>{$_("profileTransfer.import.conflictDescription")}</Alert.Description>
					</Alert.Root>
				{:else if plan.no_changes}
					<Alert.Root>
						<Alert.Title>{$_("profileTransfer.import.noChangesTitle")}</Alert.Title>
						<Alert.Description>{$_("profileTransfer.import.noChangesDescription")}</Alert.Description>
					</Alert.Root>
				{:else}
					<Alert.Root>
						<Alert.Title>{$_("profileTransfer.import.readyTitle")}</Alert.Title>
						<Alert.Description>{$_("profileTransfer.import.readyDescription")}</Alert.Description>
					</Alert.Root>
				{/if}

				<div class="max-h-72 overflow-auto rounded-md border">
					<Table.Root>
						<Table.Header>
							<Table.Row>
								<Table.Head>{$_("profileTransfer.table.kind")}</Table.Head>
								<Table.Head>{$_("profileTransfer.table.resource")}</Table.Head>
								<Table.Head>{$_("profileTransfer.table.action")}</Table.Head>
								<Table.Head>{$_("profileTransfer.table.details")}</Table.Head>
							</Table.Row>
						</Table.Header>
						<Table.Body>
							{#each plan.items as item (`${item.kind}:${item.id}`)}
								<Table.Row>
									<Table.Cell>{$_(`profileTransfer.kinds.${item.kind}`)}</Table.Cell>
									<Table.Cell>
										<div class="flex min-w-0 flex-col gap-1">
											<span class="truncate">{item.name || item.id}</span>
											{#if item.name}<span class="truncate font-mono text-xs text-muted-foreground">{item.id}</span>{/if}
										</div>
									</Table.Cell>
									<Table.Cell><Badge variant={actionVariant(item.action)}>{$_(`profileTransfer.actions.${item.action}`)}</Badge></Table.Cell>
									<Table.Cell class="max-w-72 text-sm text-muted-foreground">{item.reason ? $_(`profileTransfer.reasons.${item.reason}`) : "—"}</Table.Cell>
								</Table.Row>
							{/each}
						</Table.Body>
					</Table.Root>
				</div>
				<p class="break-all text-xs text-muted-foreground">{plan.path}</p>
			</div>
		{/if}

		<Dialog.Footer>
			<Button variant="outline" onclick={() => { open = false; onClose(); }}>{$_("actions.cancel")}</Button>
			<Button disabled={!plan?.can_apply || busy} onclick={onApply}>
				{#if busy}<Spinner data-icon="inline-start" />{/if}{$_("actions.applyImport")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
