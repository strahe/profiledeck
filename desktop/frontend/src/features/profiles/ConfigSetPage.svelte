<script lang="ts">
	import { _ } from "svelte-i18n";
	import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
	import CopyIcon from "@lucide/svelte/icons/copy";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PencilIcon from "@lucide/svelte/icons/pencil";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import TrashIcon from "@lucide/svelte/icons/trash-2";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import * as Card from "$lib/components/ui/card";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import * as Empty from "$lib/components/ui/empty";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Separator } from "$lib/components/ui/separator";
	import { Skeleton } from "$lib/components/ui/skeleton";

	import type { CodexConfigSet } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	interface Props {
		configSets: CodexConfigSet[];
		loading: boolean;
		error: string;
		busy: boolean;
		formatUpdated: (value: number) => string;
		onBack: () => void;
		onCreate: () => void;
		onCopy: (configSet: CodexConfigSet) => void;
		onEdit: (configSet: CodexConfigSet) => void;
		onDelete: (configSet: CodexConfigSet) => void;
	}

	let { configSets, loading, error, busy, formatUpdated, onBack, onCreate, onCopy, onEdit, onDelete }: Props = $props();
	let deleting = $state<CodexConfigSet | null>(null);

	function showID(configSet: CodexConfigSet): boolean {
		if (!configSet.name.trim()) return true;
		return configSets.filter((item) => item.name.trim() === configSet.name.trim()).length > 1;
	}

	function shortID(value: string): string {
		return value.length > 8 ? `…${value.slice(-8)}` : value;
	}
</script>

<ContentContainer>
	<PageHeader title={$_("configSets.title")} description={$_("configSets.description")}>
		{#snippet meta()}
			<Button variant="link" class="h-auto w-fit p-0 text-muted-foreground" onclick={onBack}>
				<ArrowLeftIcon data-icon="inline-start" />{$_("actions.backToProfiles")}
			</Button>
		{/snippet}
		{#snippet actions()}
			<Button size="sm" onclick={onCreate} disabled={busy}><PlusIcon />{$_("configSets.create")}</Button>
		{/snippet}
	</PageHeader>

	<Card.Root>
		<Card.Content class="px-0">
			{#if loading}
				<div class="flex flex-col gap-3 p-4">
					{#each [1, 2, 3] as item (item)}<Skeleton class="h-14 w-full" />{/each}
				</div>
			{:else if error}
				<div class="p-4"><Alert.Root variant="destructive"><Alert.Description>{error}</Alert.Description></Alert.Root></div>
			{:else if configSets.length === 0}
				<Empty.Root class="border-0 py-12">
					<Empty.Header><Empty.Title>{$_("configSets.emptyTitle")}</Empty.Title><Empty.Description>{$_("configSets.emptyDescription")}</Empty.Description></Empty.Header>
					<Empty.Content><Button size="sm" onclick={onCreate}><PlusIcon data-icon="inline-start" />{$_("configSets.create")}</Button></Empty.Content>
				</Empty.Root>
			{:else}
				{#each configSets as configSet, index (configSet.id)}
					<div class="flex items-center gap-4 px-4 py-3">
						<div class="flex min-w-0 flex-1 flex-col gap-1">
							<div class="flex flex-wrap items-center gap-2">
								<span class="font-medium">{configSet.name || $_("configSets.unnamed")}</span>
								{#if showID(configSet)}<span class="font-mono text-xs text-muted-foreground">{shortID(configSet.id)}</span>{/if}
								{#if configSet.active}<StatusBadge tone="current">{$_("status.current")}</StatusBadge>{/if}
								<Badge variant="outline">{$_("configSets.references", { values: { count: configSet.reference_count } })}</Badge>
							</div>
							{#if configSet.description}<p class="truncate text-sm text-muted-foreground">{configSet.description}</p>{/if}
							<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
								{#if configSet.model}<span>{configSet.model}</span>{/if}
								{#if configSet.model_provider}<span>{configSet.model_provider}</span>{/if}
								<span>{formatUpdated(configSet.updated_at_unix_ms)}</span>
							</div>
						</div>
						<DropdownMenu.Root>
							<DropdownMenu.Trigger>
								{#snippet child({ props })}<Button {...props} variant="outline" size="icon-sm" aria-label={$_("actions.more")}><MoreHorizontalIcon data-icon="inline-start" /></Button>{/snippet}
							</DropdownMenu.Trigger>
							<DropdownMenu.Content align="end">
								<DropdownMenu.Group>
									<DropdownMenu.Item onSelect={() => onCopy(configSet)}><CopyIcon data-icon="inline-start" />{$_("actions.copy")}</DropdownMenu.Item>
									<DropdownMenu.Item onSelect={() => onEdit(configSet)}><PencilIcon data-icon="inline-start" />{$_("actions.edit")}</DropdownMenu.Item>
									<DropdownMenu.Item disabled={configSet.reference_count > 0} onSelect={() => (deleting = configSet)}><TrashIcon data-icon="inline-start" />{$_("actions.delete")}</DropdownMenu.Item>
								</DropdownMenu.Group>
							</DropdownMenu.Content>
						</DropdownMenu.Root>
					</div>
					{#if index < configSets.length - 1}<Separator />{/if}
				{/each}
			{/if}
		</Card.Content>
	</Card.Root>
</ContentContainer>

<AlertDialog.Root open={!!deleting} onOpenChange={(open) => { if (!open) deleting = null; }}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{$_("configSets.deleteTitle")}</AlertDialog.Title>
			<AlertDialog.Description>{$_("configSets.deleteDescription", { values: { name: deleting?.name ?? "" } })}</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel>
			<AlertDialog.Action onclick={() => { if (deleting) onDelete(deleting); deleting = null; }}>{$_("actions.delete")}</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
