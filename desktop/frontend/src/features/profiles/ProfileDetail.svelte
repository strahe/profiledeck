<script lang="ts">
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import GitForkIcon from "@lucide/svelte/icons/git-fork";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PencilIcon from "@lucide/svelte/icons/pencil";
	import RocketIcon from "@lucide/svelte/icons/rocket";
	import SaveIcon from "@lucide/svelte/icons/save";
	import SlidersHorizontalIcon from "@lucide/svelte/icons/sliders-horizontal";

	import * as Alert from "$lib/components/ui/alert";
	import * as Breadcrumb from "$lib/components/ui/breadcrumb";
	import * as Card from "$lib/components/ui/card";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";

	import type { CodexProfileDetail, CodexProfileQuota } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import ProfileQuotaCard from "./ProfileQuotaCard.svelte";

	interface Props {
		detail: CodexProfileDetail;
		busyAction: string;
		updated: string;
		quota: CodexProfileQuota | null;
		quotaLoading: boolean;
		onUse: () => void;
		onFork: () => void;
		onEdit: () => void;
		onSaveCurrent: () => void;
		onSetConfig: () => void;
		onRefreshQuota: () => void;
	}

	let { detail, busyAction, updated, quota, quotaLoading, onUse, onFork, onEdit, onSaveCurrent, onSetConfig, onRefreshQuota }: Props = $props();
	let profileName = $derived(detail.summary.profile.name || detail.summary.profile.id);
</script>

<div class="mx-auto flex w-full max-w-5xl flex-col gap-4">
	<Breadcrumb.Root>
		<Breadcrumb.List>
			<Breadcrumb.Item><Breadcrumb.Link href="#/codex/profiles">{$_("tabs.profiles")}</Breadcrumb.Link></Breadcrumb.Item>
			<Breadcrumb.Separator />
			<Breadcrumb.Item><Breadcrumb.Page>{profileName}</Breadcrumb.Page></Breadcrumb.Item>
		</Breadcrumb.List>
	</Breadcrumb.Root>

	<div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
		<div class="flex min-w-0 flex-col gap-1">
			<div class="flex items-center gap-2">
				<h2 class="truncate text-xl font-semibold tracking-tight">{profileName}</h2>
				{#if detail.summary.active}<Badge variant="secondary">{$_("status.active")}</Badge>{/if}
			</div>
			<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
				<span class="font-mono">{detail.summary.profile.id}</span>
				<span>{updated}</span>
			</div>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			{#if !detail.summary.active}
				<Button size="sm" disabled={!!busyAction} onclick={onUse}>
					<RocketIcon data-icon="inline-start" />{$_("actions.useProfile")}
				</Button>
			{/if}
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="outline" size="icon-sm" aria-label={$_("actions.more")}>
							<MoreHorizontalIcon data-icon="inline-start" />
						</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end">
					<DropdownMenu.Item onSelect={onEdit}><PencilIcon data-icon="inline-start" />{$_("actions.editDetails")}</DropdownMenu.Item>
					<DropdownMenu.Item onSelect={onFork}><GitForkIcon data-icon="inline-start" />{$_("actions.fork")}</DropdownMenu.Item>
					{#if detail.summary.active}
						<DropdownMenu.Item onSelect={onSaveCurrent}><SaveIcon data-icon="inline-start" />{$_("actions.saveCurrent")}</DropdownMenu.Item>
					{:else}
						<DropdownMenu.Item onSelect={onSetConfig}><SlidersHorizontalIcon data-icon="inline-start" />{$_("actions.changeConfigSet")}</DropdownMenu.Item>
					{/if}
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		</div>
	</div>

	{#if detail.summary.profile.description}
		<p class="text-sm text-muted-foreground">{detail.summary.profile.description}</p>
	{/if}
	{#if detail.summary.warnings?.length}
		<Alert.Root>
			<AlertTriangleIcon data-icon="inline-start" />
			<Alert.Title>{$_("profilePages.detail.warningTitle")}</Alert.Title>
			<Alert.Description>{detail.summary.warnings.join(" ")}</Alert.Description>
		</Alert.Root>
	{/if}

	<div class="grid gap-4 lg:grid-cols-2">
		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.detail.loginState")}</Card.Title>
				<Card.Description>{$_("profilePages.detail.loginStateDescription")}</Card.Description>
			</Card.Header>
			<Card.Content>
				<dl class="grid gap-4 sm:grid-cols-2">
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.account")}</dt>
						<dd class="truncate text-sm">{detail.login?.codex_account_id || $_("profile.noAccount")}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.references")}</dt>
						<dd class="text-sm">{detail.login?.reference_count ?? 0}</dd>
					</div>
				</dl>
			</Card.Content>
		</Card.Root>

		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.detail.configSet")}</Card.Title>
				<Card.Description>{$_("profilePages.detail.configSetDescription")}</Card.Description>
			</Card.Header>
			<Card.Content>
				<dl class="grid gap-4 sm:grid-cols-2">
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.name")}</dt>
						<dd class="truncate text-sm">{detail.config_set?.name || "—"}</dd>
						<dd class="truncate font-mono text-xs text-muted-foreground">{detail.config_set?.id || "—"}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.references")}</dt>
						<dd class="text-sm">{detail.config_set?.reference_count ?? 0}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.model")}</dt>
						<dd class="truncate text-sm">{detail.config_set?.model || "—"}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.provider")}</dt>
						<dd class="truncate text-sm">{detail.config_set?.model_provider || "—"}</dd>
					</div>
				</dl>
			</Card.Content>
		</Card.Root>

		<ProfileQuotaCard {quota} loading={quotaLoading} disabled={!!busyAction} onRefresh={onRefreshQuota} />
	</div>
</div>
