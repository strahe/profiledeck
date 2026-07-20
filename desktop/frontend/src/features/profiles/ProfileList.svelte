<script lang="ts">
	import { onMount } from "svelte";
	import { _ } from "svelte-i18n";
	import CheckIcon from "@lucide/svelte/icons/check";
	import DownloadIcon from "@lucide/svelte/icons/download";
	import EyeIcon from "@lucide/svelte/icons/eye";
	import GitForkIcon from "@lucide/svelte/icons/git-fork";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import Trash2Icon from "@lucide/svelte/icons/trash-2";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import IconAction from "$lib/components/app/IconAction.svelte";
	import InfoTooltip from "$lib/components/app/InfoTooltip.svelte";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as Card from "$lib/components/ui/card";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import * as Empty from "$lib/components/ui/empty";
	import { Button } from "$lib/components/ui/button";
	import { Separator } from "$lib/components/ui/separator";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { joinUserMessages, profileWarningMessage } from "$lib/user-facing-messages";
	import { cn } from "$lib/utils";

	import ProfileQuotaFreshness from "./ProfileQuotaFreshness.svelte";
	import ProfileQuotaSummary from "./ProfileQuotaSummary.svelte";
	import type { CodexProfileListItem } from "./types";

	let {
		profiles,
		loading,
		error,
		busy,
		canCreate = false,
		onNew,
		onUse,
		onDetails,
		onFork,
		onExport,
		onDelete,
		onRefreshQuota,
		onRetrySource,
	}: {
		profiles: CodexProfileListItem[];
		loading: boolean;
		error: string;
		busy: boolean;
		canCreate?: boolean;
		onNew?: () => void;
		onUse: (profile: CodexProfileListItem) => void;
		onDetails: (profile: CodexProfileListItem) => void;
		onFork: (profile: CodexProfileListItem) => void;
		onExport: (profile: CodexProfileListItem) => void;
		onDelete: (profile: CodexProfileListItem) => void;
		onRefreshQuota: (profile: CodexProfileListItem) => void;
		onRetrySource?: () => void;
	} = $props();

	let nowUnixMS = $state(Date.now());

	onMount(() => {
		const timer = window.setInterval(() => {
			nowUnixMS = Date.now();
		}, 60_000);
		return () => window.clearInterval(timer);
	});

	function showID(profile: CodexProfileListItem): boolean {
		const name = profile.summary.profile.name.trim();
		if (!name) return true;
		return profiles.filter((item) => item.summary.profile.name.trim() === name).length > 1;
	}

	function shortID(value: string): string {
		return value.length > 8 ? `…${value.slice(-8)}` : value;
	}
</script>

<Card.Root size="sm">
	<Card.Content class="px-0">
		{#if loading}
			<div class="flex flex-col gap-3 px-4 pb-4">
				{#each [1, 2, 3] as item (item)}
					<div class="flex flex-col gap-2 py-2">
						<div class="flex items-center justify-between gap-4">
							<Skeleton class="h-4 w-40" />
							<Skeleton class="h-8 w-24" />
						</div>
						<Skeleton class="h-12 w-full" />
					</div>
				{/each}
			</div>
		{:else if error}
			<div class="px-4 pb-4">
				<Alert.Root variant="destructive">
					<TriangleAlertIcon data-icon="inline-start" />
					<Alert.Title>{$_("empty.loadProfilesFailedTitle")}</Alert.Title>
					<Alert.Description>{error}</Alert.Description>
					<Alert.Action><Button size="xs" variant="outline" onclick={onRetrySource}>{$_("actions.retry")}</Button></Alert.Action>
				</Alert.Root>
			</div>
		{:else if profiles.length === 0}
			<Empty.Root class="border-0 py-12">
				<Empty.Header>
					<Empty.Media variant="icon"><PlusIcon /></Empty.Media>
					<Empty.Title>{$_("empty.noProfilesTitle")}</Empty.Title>
					<Empty.Description>{$_("profilePages.list.emptyDescription")}</Empty.Description>
				</Empty.Header>
				{#if canCreate}
					<Empty.Content><Button size="sm" onclick={onNew}><PlusIcon />{$_("actions.saveAsNewProfile")}</Button></Empty.Content>
				{/if}
			</Empty.Root>
		{:else}
			{#each profiles as profile, index (profile.id)}
				<div class={cn("flex flex-col gap-2.5 px-4 py-3", profile.summary.active && "bg-primary/5 ring-1 ring-inset ring-primary/20") }>
					<div class="flex min-w-0 flex-col gap-2">
						<div class="flex min-w-0 items-center gap-4">
							<div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
								<Button variant="link" class="h-auto min-w-0 justify-start p-0" onclick={() => onDetails(profile)}>
									<span class="truncate">{profile.name}</span>
								</Button>
								{#if profile.description}<InfoTooltip content={profile.description} subject={profile.name} />{/if}
								{#if showID(profile)}<span class="font-mono text-xs text-muted-foreground">{shortID(profile.id)}</span>{/if}
								{#if profile.summary.active}<StatusBadge tone="current"><CheckIcon />{$_("status.current")}</StatusBadge>{/if}
								{#if profile.summary.warnings?.length}<StatusBadge tone="warning">{$_("status.warning")}</StatusBadge>{/if}
							</div>

							<div class="ml-auto flex shrink-0 items-center justify-end gap-2">
								<ProfileQuotaFreshness
									checkedAtUnixMS={profile.quotaCheckedAtUnixMS}
									checkOutcome={profile.quotaCheckOutcome}
									{nowUnixMS}
								/>
								<IconAction
									label={$_("actions.refreshProfileQuota", { values: { profile: profile.name } })}
									disabled={busy || profile.quotaLoading}
									onclick={() => onRefreshQuota(profile)}
								>
									{#if profile.quotaLoading}<Spinner />{:else}<RefreshCwIcon />{/if}
								</IconAction>
								{#if !profile.summary.active}<Button size="sm" disabled={busy} onclick={() => onUse(profile)}>{$_("actions.useProfile")}</Button>{/if}
								<DropdownMenu.Root>
									<DropdownMenu.Trigger>
										{#snippet child({ props })}
											<Button {...props} variant="outline" size="icon-sm" aria-label={$_("actions.more")}><MoreHorizontalIcon /></Button>
										{/snippet}
									</DropdownMenu.Trigger>
									<DropdownMenu.Content align="end">
										<DropdownMenu.Group>
											<DropdownMenu.Item onSelect={() => onDetails(profile)}><EyeIcon />{$_("actions.details")}</DropdownMenu.Item>
											<DropdownMenu.Item onSelect={() => onFork(profile)}><GitForkIcon />{$_("actions.fork")}</DropdownMenu.Item>
											<DropdownMenu.Item onSelect={() => onExport(profile)}><DownloadIcon />{$_("actions.exportProfile")}</DropdownMenu.Item>
											<DropdownMenu.Item variant="destructive" disabled={busy} onSelect={() => onDelete(profile)}><Trash2Icon />{$_("actions.deleteProfile")}</DropdownMenu.Item>
										</DropdownMenu.Group>
									</DropdownMenu.Content>
								</DropdownMenu.Root>
							</div>
						</div>

						<ProfileQuotaSummary
							quota={profile.quota}
							loading={profile.quotaLoading}
							{nowUnixMS}
						/>
					</div>

					{#if profile.summary.warnings?.length}
						<Alert.Root>
							<TriangleAlertIcon data-icon="inline-start" />
							<Alert.Title>{$_("profilePages.list.warningTitle")}</Alert.Title>
							<Alert.Description>{joinUserMessages(profile.summary.warnings, profileWarningMessage)}</Alert.Description>
						</Alert.Root>
					{/if}
				</div>
				{#if index < profiles.length - 1}<Separator />{/if}
			{/each}
		{/if}
	</Card.Content>
</Card.Root>
