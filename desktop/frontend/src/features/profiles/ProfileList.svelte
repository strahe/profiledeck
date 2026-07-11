<script lang="ts">
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import DownloadIcon from "@lucide/svelte/icons/download";
	import EyeIcon from "@lucide/svelte/icons/eye";
	import GitForkIcon from "@lucide/svelte/icons/git-fork";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import SlidersHorizontalIcon from "@lucide/svelte/icons/sliders-horizontal";
	import UploadIcon from "@lucide/svelte/icons/upload";

	import * as Alert from "$lib/components/ui/alert";
	import * as Card from "$lib/components/ui/card";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import * as Empty from "$lib/components/ui/empty";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Separator } from "$lib/components/ui/separator";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";

	import ProfileQuotaSummary from "./ProfileQuotaSummary.svelte";
	import type { CodexProfileListItem } from "./types";

	let {
		profiles,
		loading,
		error,
		busy,
		onNew,
		onUse,
		onDetails,
		onFork,
		onConfigSets,
		onExportAll,
		onImport,
		onExport,
		onRefreshQuota,
	}: {
		profiles: CodexProfileListItem[];
		loading: boolean;
		error: string;
		busy: boolean;
		onNew: () => void;
		onUse: (profile: CodexProfileListItem) => void;
		onDetails: (profile: CodexProfileListItem) => void;
		onFork: (profile: CodexProfileListItem) => void;
		onConfigSets: () => void;
		onExportAll: () => void;
		onImport: () => void;
		onExport: (profile: CodexProfileListItem) => void;
		onRefreshQuota: (profile: CodexProfileListItem) => void;
	} = $props();
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>{$_("profilePages.list.title")}</Card.Title>
		<Card.Description>{$_("profilePages.list.description")}</Card.Description>
		<Card.Action>
			<div class="flex items-center gap-2">
				<DropdownMenu.Root>
					<DropdownMenu.Trigger>
						{#snippet child({ props })}
							<Button {...props} size="sm" variant="outline" disabled={busy}>
								<DownloadIcon data-icon="inline-start" />
								{$_("actions.transferProfiles")}
							</Button>
						{/snippet}
					</DropdownMenu.Trigger>
					<DropdownMenu.Content align="end">
						<DropdownMenu.Group>
							<DropdownMenu.Item onSelect={onExportAll}>
								<DownloadIcon data-icon="inline-start" />
								{$_("actions.exportAllProfiles")}
							</DropdownMenu.Item>
							<DropdownMenu.Item onSelect={onImport}>
								<UploadIcon data-icon="inline-start" />
								{$_("actions.importProfiles")}
							</DropdownMenu.Item>
						</DropdownMenu.Group>
					</DropdownMenu.Content>
				</DropdownMenu.Root>
				<Button size="sm" variant="outline" onclick={onConfigSets}>
					<SlidersHorizontalIcon data-icon="inline-start" />
					{$_("actions.configSets")}
				</Button>
				<Button size="sm" onclick={onNew}>
					<PlusIcon data-icon="inline-start" />
					{$_("actions.newProfile")}
				</Button>
			</div>
		</Card.Action>
	</Card.Header>

	<Card.Content class="px-0">
		{#if loading}
			<div class="flex flex-col gap-3 px-4 pb-4">
				{#each [1, 2, 3] as item (item)}
					<div class="flex items-center gap-4 py-2">
						<div class="flex min-w-0 flex-1 flex-col gap-2">
							<Skeleton class="h-4 w-40" />
							<Skeleton class="h-3 w-64" />
							<Skeleton class="h-3 w-52" />
						</div>
						<Skeleton class="h-7 w-20" />
					</div>
				{/each}
			</div>
		{:else if error}
			<div class="px-4 pb-4">
				<Alert.Root variant="destructive">
					<AlertTriangleIcon data-icon="inline-start" />
					<Alert.Title>{$_("empty.loadProfilesFailedTitle")}</Alert.Title>
					<Alert.Description>{error}</Alert.Description>
				</Alert.Root>
			</div>
		{:else if profiles.length === 0}
			<Empty.Root class="border-0 py-12">
				<Empty.Header>
					<Empty.Media variant="icon"><PlusIcon data-icon="inline-start" /></Empty.Media>
					<Empty.Title>{$_("empty.noProfilesTitle")}</Empty.Title>
					<Empty.Description>{$_("profilePages.list.emptyDescription")}</Empty.Description>
				</Empty.Header>
				<Empty.Content>
					<Button size="sm" onclick={onNew}>
						<PlusIcon data-icon="inline-start" />
						{$_("actions.newProfile")}
					</Button>
				</Empty.Content>
			</Empty.Root>
		{:else}
			{#each profiles as profile, index (profile.id)}
				<div class="flex flex-col gap-3 px-4 py-3">
					<div class="flex items-center gap-4">
						<div class="flex min-w-0 flex-1 flex-col gap-1">
							<div class="flex items-center gap-2">
								<Button variant="link" class="h-auto min-w-0 justify-start p-0" onclick={() => onDetails(profile)}>
									<span class="truncate">{profile.name}</span>
								</Button>
								<span class="truncate font-mono text-xs text-muted-foreground">{profile.id}</span>
								{#if profile.summary.active}
									<Badge variant="secondary">{$_("status.active")}</Badge>
								{/if}
								{#if profile.summary.warnings?.length}
									<Badge variant="destructive">{$_("status.warning")}</Badge>
								{/if}
							</div>
							{#if profile.description}<p class="truncate text-sm text-muted-foreground">{profile.description}</p>{/if}
							<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
								{#if profile.account}<span>{profile.account}</span>{/if}
								{#if profile.configSet}<Badge variant="outline">{profile.configSet}</Badge>{/if}
								<span>{profile.updated}</span>
							</div>
							<ProfileQuotaSummary quota={profile.quota} loading={profile.quotaLoading} />
						</div>

						<div class="flex shrink-0 items-center gap-2">
							<Button
								size="icon-sm"
								variant="outline"
								disabled={busy || profile.quotaLoading}
								onclick={() => onRefreshQuota(profile)}
								aria-label={$_("actions.refreshProfileQuota", { values: { profile: profile.name } })}
								title={$_("actions.refreshProfileQuota", { values: { profile: profile.name } })}
							>
								{#if profile.quotaLoading}<Spinner />{:else}<RefreshCwIcon />{/if}
							</Button>
							{#if !profile.summary.active}
								<Button size="sm" disabled={busy} onclick={() => onUse(profile)}>{$_("actions.useProfile")}</Button>
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
									<DropdownMenu.Group>
										<DropdownMenu.Item onSelect={() => onDetails(profile)}>
											<EyeIcon data-icon="inline-start" />
											{$_("actions.details")}
										</DropdownMenu.Item>
									<DropdownMenu.Item onSelect={() => onFork(profile)}>
										<GitForkIcon data-icon="inline-start" />
										{$_("actions.fork")}
									</DropdownMenu.Item>
									<DropdownMenu.Item onSelect={() => onExport(profile)}>
										<DownloadIcon data-icon="inline-start" />
										{$_("actions.exportProfile")}
									</DropdownMenu.Item>
									</DropdownMenu.Group>
								</DropdownMenu.Content>
							</DropdownMenu.Root>
						</div>
					</div>

					{#if profile.summary.warnings?.length}
						<Alert.Root>
							<AlertTriangleIcon data-icon="inline-start" />
							<Alert.Title>{$_("profilePages.list.warningTitle")}</Alert.Title>
							<Alert.Description>
								{profile.summary.warnings.join(" ")}
							</Alert.Description>
						</Alert.Root>
					{/if}
				</div>
				{#if index < profiles.length - 1}<Separator />{/if}
			{/each}
		{/if}
	</Card.Content>
</Card.Root>
