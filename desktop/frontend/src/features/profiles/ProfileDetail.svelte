<script lang="ts">
	import { _ } from "svelte-i18n";
	import CheckIcon from "@lucide/svelte/icons/check";
	import DownloadIcon from "@lucide/svelte/icons/download";
	import GitForkIcon from "@lucide/svelte/icons/git-fork";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PencilIcon from "@lucide/svelte/icons/pencil";
	import RocketIcon from "@lucide/svelte/icons/rocket";
	import SaveIcon from "@lucide/svelte/icons/save";
	import SlidersHorizontalIcon from "@lucide/svelte/icons/sliders-horizontal";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import type { CodexProfileDetail, CodexProfileQuota } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as Breadcrumb from "$lib/components/ui/breadcrumb";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import { Button } from "$lib/components/ui/button";
	import { Spinner } from "$lib/components/ui/spinner";
	import { joinUserMessages, profileWarningMessage } from "$lib/user-facing-messages";
	import ProfileAutomationSettings from "../settings/ProfileAutomationSettings.svelte";
	import { useCodexRuntime } from "../settings/codex-runtime.svelte.js";
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
		onExport: () => void;
		onSaveCurrent: () => void;
		onSetConfig: () => void;
		onRefreshQuota: () => void;
	}

	let { detail, busyAction, updated, quota, quotaLoading, onUse, onFork, onEdit, onExport, onSaveCurrent, onSetConfig, onRefreshQuota }: Props = $props();
	const runtime = useCodexRuntime();
	let profileName = $derived(detail.summary.profile.name || detail.summary.profile.id);
	let automation = $derived(runtime.settingsProfile(detail.summary.profile.id));

	function accountSuffix(value: string | undefined): string {
		if (!value) return "—";
		return value.length > 8 ? `…${value.slice(-8)}` : value;
	}
</script>

<ContentContainer>
	<PageHeader title={profileName} description={detail.summary.profile.description || ""}>
		{#snippet breadcrumbs()}
			<Breadcrumb.Root>
				<Breadcrumb.List>
					<Breadcrumb.Item><Breadcrumb.Link href="#/codex/profiles">{$_("tabs.profiles")}</Breadcrumb.Link></Breadcrumb.Item>
					<Breadcrumb.Separator />
					<Breadcrumb.Item><Breadcrumb.Page>{profileName}</Breadcrumb.Page></Breadcrumb.Item>
				</Breadcrumb.List>
			</Breadcrumb.Root>
		{/snippet}
		{#snippet status()}
			{#if detail.summary.active}<StatusBadge tone="current"><CheckIcon />{$_("status.current")}</StatusBadge>{/if}
			{#if detail.summary.warnings?.length}<StatusBadge tone="warning">{$_("status.warning")}</StatusBadge>{/if}
		{/snippet}
		{#snippet meta()}
			<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
				{#if detail.summary.profile.name && detail.summary.profile.name !== detail.summary.profile.id}
					<span class="font-mono">{detail.summary.profile.id}</span>
				{/if}
				<span>{updated}</span>
			</div>
		{/snippet}
		{#snippet actions()}
			{#if !detail.summary.active}
				<Button size="sm" disabled={!!busyAction} onclick={onUse}><RocketIcon />{$_("actions.useProfile")}</Button>
			{/if}
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="outline" size="icon-sm" aria-label={$_("actions.more")}><MoreHorizontalIcon /></Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end">
					<DropdownMenu.Group>
						<DropdownMenu.Item onSelect={onEdit}><PencilIcon />{$_("actions.editDetails")}</DropdownMenu.Item>
						<DropdownMenu.Item onSelect={onFork}><GitForkIcon />{$_("actions.fork")}</DropdownMenu.Item>
						<DropdownMenu.Item onSelect={onExport}><DownloadIcon />{$_("actions.exportProfile")}</DropdownMenu.Item>
						{#if detail.summary.active}
							<DropdownMenu.Item onSelect={onSaveCurrent}><SaveIcon />{$_("actions.updateFromCurrent")}</DropdownMenu.Item>
						{:else}
							<DropdownMenu.Item onSelect={onSetConfig}><SlidersHorizontalIcon />{$_("actions.changeConfigSet")}</DropdownMenu.Item>
						{/if}
					</DropdownMenu.Group>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		{/snippet}
	</PageHeader>

	{#if detail.summary.warnings?.length}
		<Alert.Root>
			<TriangleAlertIcon data-icon="inline-start" />
			<Alert.Title>{$_("profilePages.detail.warningTitle")}</Alert.Title>
			<Alert.Description>{joinUserMessages(detail.summary.warnings, profileWarningMessage)}</Alert.Description>
		</Alert.Root>
	{/if}

	<SectionCard title={$_("profilePages.detail.overview")}>
		<dl class="grid gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-3">
			<div class="flex min-w-0 flex-col gap-1">
				<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.configSet")}</dt>
				<dd class="truncate text-sm font-medium">{detail.config_set?.name || "—"}</dd>
				<dd class="truncate font-mono text-xs text-muted-foreground">{detail.config_set?.id || "—"}</dd>
			</div>
			<div class="flex min-w-0 flex-col gap-1">
				<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.model")}</dt>
				<dd class="truncate text-sm">{detail.config_set?.model || "—"}</dd>
			</div>
			<div class="flex min-w-0 flex-col gap-1">
				<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.provider")}</dt>
				<dd class="truncate text-sm">{detail.config_set?.model_provider || "—"}</dd>
			</div>
			<div class="flex min-w-0 flex-col gap-1">
				<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.accountMetadata")}</dt>
				<dd class="font-mono text-sm">{accountSuffix(detail.login?.codex_account_id)}</dd>
				<dd class="text-xs text-muted-foreground">{$_("profilePages.detail.accountMetadataDescription")}</dd>
			</div>
			<div class="flex min-w-0 flex-col gap-1">
				<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.credentialReferences")}</dt>
				<dd class="text-sm">{detail.login?.reference_count ?? 0}</dd>
			</div>
			<div class="flex min-w-0 flex-col gap-1">
				<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.configReferences")}</dt>
				<dd class="text-sm">{detail.config_set?.reference_count ?? 0}</dd>
			</div>
		</dl>
	</SectionCard>

	<ProfileQuotaCard {quota} loading={quotaLoading} disabled={!!busyAction} onRefresh={onRefreshQuota} />

	<SectionCard title={$_("profilePages.detail.automation")} description={$_("profilePages.detail.automationDescription")}>
		{#if runtime.loading}
			<div class="flex justify-center py-6"><Spinner /></div>
		{:else if automation}
			<ProfileAutomationSettings profile={automation} showName={false} />
		{:else}
			<p class="text-sm text-muted-foreground">{$_("profilePages.detail.automationUnavailable")}</p>
		{/if}
	</SectionCard>
</ContentContainer>
