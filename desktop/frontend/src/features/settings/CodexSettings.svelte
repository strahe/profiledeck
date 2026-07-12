<script lang="ts">
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import ClockIcon from "@lucide/svelte/icons/clock-3";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as Empty from "$lib/components/ui/empty";
	import * as Select from "$lib/components/ui/select";
	import { Separator } from "$lib/components/ui/separator";
	import { Spinner } from "$lib/components/ui/spinner";

	import ProfileAutomationSettings from "./ProfileAutomationSettings.svelte";
	import { usageIntervals, useCodexRuntime } from "./codex-runtime.svelte.js";

	const controller = useCodexRuntime();

	function identifierHint(profileID: string, profileName: string): string {
		const duplicate = !!profileName && (controller.settings?.profiles ?? []).filter((profile) => profile.profile_name === profileName).length > 1;
		if (profileName && !duplicate) return "";
		return profileID.length > 8 ? `…${profileID.slice(-8)}` : profileID;
	}
</script>

<ContentContainer class="max-w-4xl">
	{#if controller.runtime.app_server_status === "unavailable" || controller.runtime.app_server_status === "incompatible"}
		<Alert.Root variant="destructive">
			<AlertTriangleIcon data-icon="inline-start" />
			<Alert.Title>{$_("codexSettings.appServer.title")}</Alert.Title>
			<Alert.Description>
				{controller.runtime.app_server_status === "incompatible" ? $_("codexSettings.appServer.incompatible") : $_("codexSettings.appServer.unavailable")}
			</Alert.Description>
		</Alert.Root>
	{/if}

	<SectionCard title={$_("codexSettings.usageSync.title")} description={$_("codexSettings.usageSync.description")}>
		<SettingsRow label={$_("codexSettings.usageSync.label")} forID="codex-usage-sync">
			{#snippet control()}
				{#if controller.isBusy("usage")}<Spinner />{/if}
				<Select.Root
					type="single"
					value={String(controller.settings?.usage_sync_interval_seconds ?? 15)}
					onValueChange={(value) => controller.changeUsageSyncInterval(value)}
				>
					<Select.Trigger id="codex-usage-sync" class="min-w-32" disabled={controller.loading || controller.isBusy("usage")}>
						{$_("codexSettings.usageSync.seconds", { values: { count: controller.settings?.usage_sync_interval_seconds ?? 15 } })}
					</Select.Trigger>
					<Select.Content><Select.Group>
						{#each usageIntervals as seconds (seconds)}
							<Select.Item value={String(seconds)} label={$_("codexSettings.usageSync.seconds", { values: { count: seconds } })} />
						{/each}
					</Select.Group></Select.Content>
				</Select.Root>
			{/snippet}
		</SettingsRow>
	</SectionCard>

	<SectionCard title={$_("codexSettings.profiles.title")} description={$_("codexSettings.profiles.description")} contentClass="px-0">
		{#if controller.loading}
			<div class="flex justify-center py-10"><Spinner /></div>
		{:else if !controller.settings?.profiles?.length}
			<Empty.Root class="border-0 py-10">
				<Empty.Header>
					<Empty.Media variant="icon"><ClockIcon /></Empty.Media>
					<Empty.Title>{$_("codexSettings.profiles.emptyTitle")}</Empty.Title>
					<Empty.Description>{$_("codexSettings.profiles.emptyDescription")}</Empty.Description>
				</Empty.Header>
			</Empty.Root>
		{:else}
			{#each controller.settings.profiles as profile, index (profile.profile_id)}
				<div class="px-4 py-4">
					<ProfileAutomationSettings {profile} identifierHint={identifierHint(profile.profile_id, profile.profile_name)} />
				</div>
				{#if index < controller.settings.profiles.length - 1}<Separator />{/if}
			{/each}
		{/if}
	</SectionCard>
</ContentContainer>
