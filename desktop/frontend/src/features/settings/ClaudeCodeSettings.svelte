<script lang="ts">
	import { onMount } from "svelte";
	import type { CancellablePromise } from "@wailsio/runtime";
	import { _ } from "svelte-i18n";

	import { ClaudeCodeService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { Settings } from "../../../bindings/github.com/strahe/profiledeck/internal/claudecode/models";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import { Button } from "$lib/components/ui/button";
	import * as Empty from "$lib/components/ui/empty";
	import { isCancelError } from "$lib/desktop-errors";
	import { translate } from "$lib/i18n";

	import UsageSyncSettings from "./UsageSyncSettings.svelte";
	import { usageIntervals, usageSyncIntervalDefault } from "./usage-sync-settings";

	let {
		providerExists,
		onOpenProfiles,
		showError,
		showNotice,
	}: {
		providerExists: boolean;
		onOpenProfiles: () => void;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	} = $props();

	let settings = $state<Settings | null>(null);
	let loading = $state(false);
	let busy = $state(false);
	let loadStarted = $state(false);
	let request: CancellablePromise<Settings> | null = null;

	onMount(() => {
		return () => request?.cancel("Claude Code settings unmounted");
	});

	$effect(() => {
		if (providerExists && !loadStarted) {
			loadStarted = true;
			void load();
		} else if (!providerExists && loadStarted) {
			request?.cancel("Claude Code provider unavailable");
			request = null;
			settings = null;
			loading = false;
			loadStarted = false;
		}
	});

	async function load() {
		request?.cancel("Claude Code settings request replaced");
		loading = true;
		const next = ClaudeCodeService.GetSettings();
		request = next;
		try {
			const value = await next;
			if (request === next) settings = value;
		} catch (error) {
			if (request === next && !isCancelError(error)) showError(error);
		} finally {
			if (request === next) {
				request = null;
				loading = false;
			}
		}
	}

	async function changeInterval(value: string) {
		const interval = Number(value);
		if (
			busy
			|| !usageIntervals.includes(interval as (typeof usageIntervals)[number])
			|| settings?.usage_sync_interval_seconds === interval
		) return;
		busy = true;
		request?.cancel("Claude Code settings update replaced");
		const next = ClaudeCodeService.UpdateSettings({ usage_sync_interval_seconds: interval });
		request = next;
		try {
			const value = await next;
			if (request !== next) return;
			settings = value;
			showNotice(
				translate("notice.settingsSaved.title"),
				translate("notice.settingsSaved.claudeCodeDescription"),
			);
		} catch (error) {
			if (request === next && !isCancelError(error)) {
				showError(error);
				void load();
			}
		} finally {
			if (request === next) request = null;
			busy = false;
		}
	}
</script>

<ContentContainer class="max-w-4xl">
	{#if !providerExists}
		<Empty.Root class="rounded-lg border bg-card py-12">
			<Empty.Header>
				<Empty.Title>{$_("usage.providerUnavailableTitle")}</Empty.Title>
				<Empty.Description>
					{$_("usage.providerUnavailableDescription", { values: { provider: "Claude Code" } })}
				</Empty.Description>
			</Empty.Header>
			<Empty.Content>
				<Button onclick={onOpenProfiles}>{$_("usage.openProfiles")}</Button>
			</Empty.Content>
		</Empty.Root>
	{:else}
		<UsageSyncSettings
			id="claude-code-usage-sync"
			interval={settings?.usage_sync_interval_seconds ?? usageSyncIntervalDefault}
			{loading}
			{busy}
			onChange={changeInterval}
		/>
	{/if}
</ContentContainer>
