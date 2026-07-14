<script lang="ts">
	import { _ } from "svelte-i18n";

	import type { CodexProfileSettings } from "../../../bindings/github.com/strahe/profiledeck/internal/codex/models";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Select from "$lib/components/ui/select";
	import * as Switch from "$lib/components/ui/switch";
	import { Spinner } from "$lib/components/ui/spinner";
	import { currentDesktopLocale, translate } from "$lib/i18n";

	import { quotaIntervals, useCodexRuntime } from "./codex-runtime.svelte.js";

	let {
		profile,
		showName = true,
		identifierHint = "",
	}: {
		profile: CodexProfileSettings;
		showName?: boolean;
		identifierHint?: string;
	} = $props();

	const controller = useCodexRuntime();
	let profileRuntime = $derived(controller.runtimeProfile(profile.profile_id));

	function quotaIntervalLabel(seconds: number): string {
		if (seconds === 0) return translate("codexSettings.quota.off");
		return translate("codexSettings.quota.minutes", { count: seconds / 60 });
	}

	function formatTimestamp(value: number | undefined): string {
		if (!value) return translate("codexSettings.runtime.never");
		return new Date(value).toLocaleString(currentDesktopLocale(), {
			month: "short",
			day: "numeric",
			hour: "2-digit",
			minute: "2-digit",
		});
	}

	function runtimeLabel(): string {
		if (profileRuntime?.running) return translate("codexSettings.runtime.running");
		if (!profileRuntime || !profileRuntime.last_completed_at_unix_ms) return translate("codexSettings.runtime.idle");
		if (profileRuntime.error_code === "AUTH_PERMANENT") return translate("codexSettings.runtime.authPermanent");
		switch (profileRuntime.status) {
			case "available": return profileRuntime.last_task === "keepalive" ? translate("codexSettings.runtime.keptAlive") : translate("codexSettings.runtime.available");
			case "auth_required": return translate("codexSettings.runtime.authRequired");
			case "unsupported": return translate("codexSettings.runtime.unsupported");
			default: return translate("codexSettings.runtime.unavailable");
		}
	}

	function authModeLabel(value: string): string {
		return value === "chatgptAuthTokens"
			? translate("codexSettings.authMode.external")
			: translate("codexSettings.authMode.unsupported");
	}
</script>

<div class="flex flex-col gap-2.5">
	<div class="flex flex-wrap items-center justify-between gap-2">
		{#if showName}
			<div class="flex min-w-0 flex-wrap items-center gap-2">
				<span class="truncate font-medium">{profile.profile_name || $_("profile.unnamed")}</span>
				{#if identifierHint}<span class="font-mono text-xs text-muted-foreground">{identifierHint}</span>{/if}
				{#if profile.auth_mode && profile.auth_mode !== "chatgpt"}
					<StatusBadge tone="warning">{authModeLabel(profile.auth_mode)}</StatusBadge>
				{/if}
			</div>
		{/if}
		<div class="flex flex-wrap items-center justify-end gap-x-2 gap-y-1">
			<span class="text-xs text-muted-foreground">
				{$_("codexSettings.runtime.lastRun")}: {formatTimestamp(profileRuntime?.last_completed_at_unix_ms)}
				<span aria-hidden="true"> · </span>
				{$_("codexSettings.runtime.nextRun")}: {formatTimestamp(profileRuntime?.next_run_at_unix_ms)}
			</span>
			<StatusBadge tone={profileRuntime?.running ? "running" : "neutral"}>{runtimeLabel()}</StatusBadge>
		</div>
	</div>

	<div class="grid gap-x-6 gap-y-5 lg:grid-cols-2">
		<SettingsRow
			label={$_("codexSettings.quota.label")}
			description={$_("codexSettings.quota.description")}
			message={profile.quota_supported ? "" : $_("codexSettings.quota.unsupported")}
			forID={`quota-interval-${profile.profile_id}`}
			disabled={!profile.quota_supported}
		>
			{#snippet control()}
				{#if controller.isBusy(`quota:${profile.profile_id}`)}<Spinner />{/if}
				<Select.Root
					type="single"
					value={String(profile.quota_refresh_interval_seconds)}
					onValueChange={(value) => controller.changeQuotaInterval(profile, value)}
				>
					<Select.Trigger
						id={`quota-interval-${profile.profile_id}`}
						class="min-w-32"
						disabled={controller.loading || (!profile.quota_supported && profile.quota_refresh_interval_seconds === 0) || controller.isBusy(`quota:${profile.profile_id}`)}
					>
						{quotaIntervalLabel(profile.quota_refresh_interval_seconds)}
					</Select.Trigger>
					<Select.Content><Select.Group>
						{#each quotaIntervals as seconds (seconds)}
							<Select.Item value={String(seconds)} label={quotaIntervalLabel(seconds)} />
						{/each}
					</Select.Group></Select.Content>
				</Select.Root>
			{/snippet}
		</SettingsRow>

		<SettingsRow
			label={$_("codexSettings.keepalive.label")}
			description={$_("codexSettings.keepalive.description")}
			message={profile.auth_keepalive_supported ? "" : $_("codexSettings.keepalive.unsupported")}
			forID={`keepalive-${profile.profile_id}`}
			disabled={!profile.auth_keepalive_supported}
		>
			{#snippet control()}
				{#if controller.isBusy(`keepalive:${profile.profile_id}`)}<Spinner />{/if}
				<Switch.Root
					id={`keepalive-${profile.profile_id}`}
					checked={profile.auth_keepalive_enabled}
					disabled={controller.loading || (!profile.auth_keepalive_supported && !profile.auth_keepalive_enabled) || controller.isBusy(`keepalive:${profile.profile_id}`)}
					onCheckedChange={(value) => controller.changeKeepalive(profile, value)}
				/>
			{/snippet}
		</SettingsRow>
	</div>
</div>
