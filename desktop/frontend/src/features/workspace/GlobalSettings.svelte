<script lang="ts">
	import { _ } from "svelte-i18n";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import { Button } from "$lib/components/ui/button";
	import * as Field from "$lib/components/ui/field";
	import { Progress } from "$lib/components/ui/progress";
	import * as Select from "$lib/components/ui/select";
	import { Spinner } from "$lib/components/ui/spinner";
	import * as Switch from "$lib/components/ui/switch";
	import type { UpdateStatus } from "../../../bindings/github.com/strahe/profiledeck/desktop/update";
	import { currentDesktopLocale, type DesktopLanguage } from "$lib/i18n";
	import AppBackupSettings from "./AppBackupSettings.svelte";

	let {
		section,
		language,
		appearance,
		languageBusy,
		appearanceBusy,
		updateStatus,
		updateBusy,
		onLanguageChange,
		onAppearanceChange,
		onChannelChange,
		onAutomaticChange,
		onCheckForUpdates,
		onRestart,
		automaticBackups,
		databaseHealthy,
		onAutomaticBackupsChange,
	}: {
		section: "general" | "backups";
		language: DesktopLanguage;
		appearance: "system" | "light" | "dark";
		languageBusy: boolean;
		appearanceBusy: boolean;
		updateStatus: UpdateStatus;
		updateBusy: string;
		onLanguageChange: (value: string) => void | Promise<void>;
		onAppearanceChange: (value: string) => void | Promise<void>;
		onChannelChange: (value: string) => void | Promise<void>;
		onAutomaticChange: (enabled: boolean) => void | Promise<void>;
		onCheckForUpdates: () => void | Promise<void>;
		onRestart: () => void | Promise<void>;
		automaticBackups: boolean;
		databaseHealthy: boolean;
		onAutomaticBackupsChange: (enabled: boolean) => void;
	} = $props();

	let updateActive = $derived(["checking", "downloading", "verifying", "preparing"].includes(updateStatus.state));
	let channelChangeDisabled = $derived(!["idle", "up_to_date", "error"].includes(updateStatus.state));
	let downloadPercent = $derived(updateStatus.total_bytes > 0
		? Math.min(100, Math.max(0, (updateStatus.downloaded_bytes / updateStatus.total_bytes) * 100))
		: 0);

	function updateStateDescription(): string {
		switch (updateStatus.state) {
			case "unavailable":
				return updateStatus.error_code === "configuration_invalid"
					? $_("settings.updates.state.configurationInvalid")
					: $_("settings.updates.state.unavailable");
			case "checking": return $_("settings.updates.state.checking");
			case "up_to_date": return $_("settings.updates.state.upToDate");
			case "downloading": return $_("settings.updates.state.downloading", {
				values: { downloaded: formatBytes(updateStatus.downloaded_bytes), total: formatBytes(updateStatus.total_bytes) },
			});
			case "verifying": return $_("settings.updates.state.verifying");
			case "preparing": return $_("settings.updates.state.preparing");
			case "ready": return $_("settings.updates.state.ready", { values: { version: updateStatus.available_version } });
			case "error": return updateErrorDescription();
			default: return $_("settings.updates.state.idle");
		}
	}

	function updateChannelDescription(): string {
		return channelChangeDisabled
			? $_("settings.updates.channel.busy")
			: $_("settings.updates.channel.description");
	}

	function updateErrorDescription(): string {
		switch (updateStatus.error_code) {
			case "feed_unavailable": return $_("settings.updates.error.unavailable");
			case "feed_invalid":
				return $_("settings.updates.error.feedRejected");
			case "artifact_verification_failed": return $_("settings.updates.error.artifactRejected");
			case "restart_failed": return $_("settings.updates.error.restartFailed");
			default: return $_("settings.updates.error.generic");
		}
	}

	function formatBytes(value: number): string {
		if (!Number.isFinite(value) || value <= 0) return "0 MB";
		return `${(value / (1024 * 1024)).toFixed(value >= 10 * 1024 * 1024 ? 0 : 1)} MB`;
	}

	function lastCheckedDescription(): string {
		if (!updateStatus.last_checked_at_unix_ms) return $_("settings.updates.neverChecked");
		return $_("settings.updates.lastChecked", {
			values: { time: new Date(updateStatus.last_checked_at_unix_ms).toLocaleString(currentDesktopLocale()) },
		});
	}
</script>

<ContentContainer class="max-w-3xl">
	{#if section === "general"}
		<SectionCard title={$_("settings.preferences.title")}>
			<Field.FieldGroup>
				<SettingsRow label={$_("settings.language.label")} description={$_("settings.language.description")} forID="desktop-language">
					{#snippet control()}
						{#if languageBusy}<Spinner />{/if}
						<Select.Root type="single" value={language} onValueChange={onLanguageChange}>
							<Select.Trigger id="desktop-language" class="min-w-36" disabled={languageBusy}>
								{language === "zh-CN" ? $_("settings.language.zhCN") : language === "en-US" ? $_("settings.language.enUS") : $_("settings.language.auto")}
							</Select.Trigger>
							<Select.Content><Select.Group>
								<Select.Item value="auto" label={$_("settings.language.auto")} />
								<Select.Item value="zh-CN" label={$_("settings.language.zhCN")} />
								<Select.Item value="en-US" label={$_("settings.language.enUS")} />
							</Select.Group></Select.Content>
						</Select.Root>
					{/snippet}
				</SettingsRow>

				<SettingsRow label={$_("settings.appearance.label")} description={$_("settings.appearance.description")} forID="desktop-appearance">
					{#snippet control()}
						{#if appearanceBusy}<Spinner />{/if}
						<Select.Root type="single" value={appearance} onValueChange={onAppearanceChange}>
							<Select.Trigger id="desktop-appearance" class="min-w-36" disabled={appearanceBusy}>
								{appearance === "dark" ? $_("settings.appearance.dark") : appearance === "light" ? $_("settings.appearance.light") : $_("settings.appearance.system")}
							</Select.Trigger>
							<Select.Content><Select.Group>
								<Select.Item value="system" label={$_("settings.appearance.system")} />
								<Select.Item value="light" label={$_("settings.appearance.light")} />
								<Select.Item value="dark" label={$_("settings.appearance.dark")} />
							</Select.Group></Select.Content>
						</Select.Root>
					{/snippet}
				</SettingsRow>
			</Field.FieldGroup>
		</SectionCard>

		<SectionCard title={$_("settings.updates.title")} description={$_("settings.updates.description")}>
			{#if updateStatus.configured}
				<Field.FieldGroup>
					<SettingsRow
						label={$_("settings.updates.channel.label")}
						description={updateChannelDescription()}
						forID="desktop-update-channel"
						disabled={channelChangeDisabled}
					>
						{#snippet control()}
							{#if updateBusy === "channel"}<Spinner />{/if}
							<Select.Root type="single" value={updateStatus.channel} onValueChange={onChannelChange}>
								<Select.Trigger
									id="desktop-update-channel"
									class="min-w-28"
									disabled={channelChangeDisabled || !!updateBusy}
								>
									{updateStatus.channel === "beta"
										? $_("settings.updates.channel.beta")
										: $_("settings.updates.channel.stable")}
								</Select.Trigger>
								<Select.Content><Select.Group>
									<Select.Item value="stable" label={$_("settings.updates.channel.stable")} />
									<Select.Item value="beta" label={$_("settings.updates.channel.beta")} />
								</Select.Group></Select.Content>
							</Select.Root>
						{/snippet}
					</SettingsRow>

					<SettingsRow
						label={$_("settings.updates.automatic.label")}
						description={$_("settings.updates.automatic.description")}
						forID="desktop-automatic-updates"
					>
						{#snippet control()}
							{#if updateBusy === "automatic"}<Spinner />{/if}
							<Switch.Root
								id="desktop-automatic-updates"
								checked={updateStatus.automatic}
								disabled={!!updateBusy}
								onCheckedChange={onAutomaticChange}
							/>
						{/snippet}
					</SettingsRow>

					<SettingsRow
						label={$_("settings.updates.status")}
						description={updateStateDescription()}
						message={`${$_("settings.updates.currentVersion", { values: { version: updateStatus.current_version } })} · ${lastCheckedDescription()}`}
					>
						{#snippet control()}
							{#if updateStatus.state === "ready"}
								<Button size="sm" disabled={!!updateBusy} onclick={onRestart}>
									{#if updateBusy === "restart"}<Spinner />{/if}
									{$_("actions.restartNow")}
								</Button>
							{:else}
								<Button size="sm" variant="outline" disabled={updateActive || !!updateBusy} onclick={onCheckForUpdates}>
									{#if updateActive || updateBusy === "check"}<Spinner />{/if}
									{$_("actions.checkForUpdates")}
								</Button>
							{/if}
						{/snippet}
					</SettingsRow>
				</Field.FieldGroup>

				{#if updateStatus.state === "downloading"}
					<Progress class="mt-4" value={downloadPercent} aria-label={$_("settings.updates.downloadProgress", { values: { value: Math.round(downloadPercent) } })} />
				{/if}
			{:else}
				<Field.FieldGroup>
					<SettingsRow
						label={$_("settings.updates.status")}
						description={updateStateDescription()}
						message={$_("settings.updates.currentVersion", { values: { version: updateStatus.current_version } })}
					>
						{#snippet control()}
							<Button size="sm" variant="outline" disabled>{$_("actions.checkForUpdates")}</Button>
						{/snippet}
					</SettingsRow>
				</Field.FieldGroup>
			{/if}
		</SectionCard>
	{:else}
		<AppBackupSettings {automaticBackups} {databaseHealthy} {onAutomaticBackupsChange} />
	{/if}
</ContentContainer>
