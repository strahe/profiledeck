<script lang="ts">
	import { _ } from "svelte-i18n";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import * as Field from "$lib/components/ui/field";
	import * as Select from "$lib/components/ui/select";
	import { Spinner } from "$lib/components/ui/spinner";
	import type { DesktopLanguage } from "$lib/i18n";

	let {
		language,
		appearance,
		languageBusy,
		appearanceBusy,
		onLanguageChange,
		onAppearanceChange,
	}: {
		language: DesktopLanguage;
		appearance: "system" | "light" | "dark";
		languageBusy: boolean;
		appearanceBusy: boolean;
		onLanguageChange: (value: string) => void | Promise<void>;
		onAppearanceChange: (value: string) => void | Promise<void>;
	} = $props();
</script>

<ContentContainer class="max-w-3xl">
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
</ContentContainer>
