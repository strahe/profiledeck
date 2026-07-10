<script lang="ts">
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import GitForkIcon from "@lucide/svelte/icons/git-fork";
	import PencilIcon from "@lucide/svelte/icons/pencil";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import RocketIcon from "@lucide/svelte/icons/rocket";

	import * as Accordion from "$lib/components/ui/accordion";
	import * as Alert from "$lib/components/ui/alert";
	import * as Breadcrumb from "$lib/components/ui/breadcrumb";
	import * as Card from "$lib/components/ui/card";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Separator } from "$lib/components/ui/separator";

	import type { CodexProfileDetail } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	let {
		detail,
		busyAction,
		updated,
		onUse,
		onFork,
		onEdit,
		onSync,
	}: {
		detail: CodexProfileDetail;
		busyAction: string;
		updated: string;
		onUse: () => void;
		onFork: () => void;
		onEdit: () => void;
		onSync: () => void;
	} = $props();

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

	<div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
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
		<div class="flex shrink-0 flex-wrap justify-start gap-2 lg:justify-end">
			{#if !detail.summary.active}
				<Button size="sm" disabled={!!busyAction} onclick={onUse}>
					<RocketIcon data-icon="inline-start" />
					{$_("actions.useProfile")}
				</Button>
			{/if}
			<Button size="sm" variant="outline" disabled={!!busyAction} onclick={onFork}>
				<GitForkIcon data-icon="inline-start" />
				{$_("actions.fork")}
			</Button>
			<Button size="sm" variant="outline" disabled={!!busyAction} onclick={onEdit}>
				<PencilIcon data-icon="inline-start" />
				{$_("actions.editDetails")}
			</Button>
			<Button size="sm" variant="outline" disabled={!!busyAction} onclick={onSync}>
				<RefreshCwIcon data-icon="inline-start" />
				{$_("actions.updateFromCurrent")}
			</Button>
		</div>
	</div>

	{#if detail.summary.warnings?.length}
		<Alert.Root>
			<AlertTriangleIcon data-icon="inline-start" />
			<Alert.Title>{$_("profilePages.detail.warningTitle")}</Alert.Title>
			<Alert.Description>{detail.summary.warnings.join(" ")}</Alert.Description>
		</Alert.Root>
	{/if}

	<Card.Root>
		<Card.Header>
			<Card.Title>{$_("profilePages.detail.overview")}</Card.Title>
			<Card.Description>{$_("profilePages.detail.overviewDescription")}</Card.Description>
		</Card.Header>
		<Card.Content>
			<dl class="flex flex-col">
				<div class="grid grid-cols-2 gap-6 pb-3">
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.name")}</dt>
						<dd class="truncate text-sm">{profileName}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.form.description")}</dt>
						<dd class="text-sm">{detail.summary.profile.description || $_("profile.noDescription")}</dd>
					</div>
				</div>
				<Separator />
				<div class="grid grid-cols-2 gap-6 py-3">
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.model")}</dt>
						<dd class="truncate text-sm">{detail.summary.model || "—"}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.provider")}</dt>
						<dd class="truncate text-sm">{detail.summary.model_provider || "—"}</dd>
					</div>
				</div>
				<Separator />
				<div class="grid grid-cols-2 gap-6 py-3">
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.baseURL")}</dt>
						<dd class="truncate font-mono text-sm">{detail.summary.openai_base_url || "—"}</dd>
					</div>
					<div class="flex min-w-0 flex-col gap-1">
						<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.account")}</dt>
						<dd class="truncate text-sm">{detail.summary.codex_account_id || $_("profile.noAccount")}</dd>
					</div>
				</div>
				<Separator />
				<div class="flex min-w-0 flex-col gap-1 pt-3">
					<dt class="text-xs text-muted-foreground">{$_("profilePages.detail.targetCount")}</dt>
					<dd class="text-sm">{detail.targets?.length ?? 0}</dd>
				</div>
			</dl>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title>{$_("profilePages.detail.targets")}</Card.Title>
			<Card.Description>{$_("profilePages.detail.targetsDescription")}</Card.Description>
		</Card.Header>
		<Card.Content>
			<Accordion.Root type="multiple">
				{#each detail.targets ?? [] as target (target.target_id)}
					<Accordion.Item value={target.target_id}>
						<Accordion.Trigger>
							<div class="flex min-w-0 flex-1 items-center gap-2 pr-2">
								<span class="shrink-0 font-medium">{target.target_id}</span>
								<span class="truncate font-mono text-xs text-muted-foreground">{target.path}</span>
								<Badge variant={target.enabled ? "secondary" : "outline"}>
									{target.enabled ? $_("status.enabled") : $_("status.disabled")}
								</Badge>
							</div>
						</Accordion.Trigger>
						<Accordion.Content>
							<div class="flex flex-col gap-3">
								<div class="flex flex-wrap items-center gap-2">
									<Badge variant="outline">{target.format}</Badge>
									<Badge variant="outline">{target.strategy}</Badge>
								</div>
								<Separator />
								<pre class="max-h-52 overflow-auto rounded-lg bg-muted p-3 text-xs">{target.value_preview.content || "—"}</pre>
								{#if target.value_preview.truncated}<span class="text-xs text-muted-foreground">{$_("useDialog.truncated")}</span>{/if}
							</div>
						</Accordion.Content>
					</Accordion.Item>
				{/each}
			</Accordion.Root>
		</Card.Content>
	</Card.Root>
</div>
