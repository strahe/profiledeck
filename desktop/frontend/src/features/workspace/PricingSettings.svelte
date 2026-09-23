<script lang="ts">
	import { onMount } from "svelte";
	import { _ } from "svelte-i18n";
	import { UsageService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { Status } from "../../../bindings/github.com/strahe/profiledeck/internal/pricing/models";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import { Button } from "$lib/components/ui/button";
	import * as Field from "$lib/components/ui/field";
	import { Spinner } from "$lib/components/ui/spinner";
	import * as Switch from "$lib/components/ui/switch";

	let status = $state<Status | null>(null);
	let busy = $state("");
	let failed = $state(false);
	let checkResult = $state<"updated" | "current" | null>(null);

	async function load() {
		try {
			status = await UsageService.PricingStatus();
			failed = false;
		} catch {
			failed = true;
		}
	}

	onMount(() => {
		void load();
		const timer = setInterval(() => { if (!busy) void load(); }, 60_000);
		return () => clearInterval(timer);
	});

	async function check() {
		busy = "check";
		const previousVersion = status?.catalog_version ?? 0;
		try {
			status = await UsageService.CheckPricing();
			failed = false;
			checkResult = status.last_error ? null : status.catalog_version > previousVersion ? "updated" : "current";
		} catch {
			failed = true;
			checkResult = null;
		} finally {
			busy = "";
		}
	}

	async function setAutomatic(enabled: boolean) {
		busy = "automatic";
		checkResult = null;
		try {
			status = await UsageService.SetAutomaticPricing(enabled);
			failed = false;
		} catch {
			failed = true;
		} finally {
			busy = "";
		}
	}

	function description(): string {
		if (failed) return $_("settings.pricing.settingsUnavailable");
		if (status?.last_error) return $_("settings.pricing.checkFailed");
		if (checkResult) return $_(`settings.pricing.${checkResult}`);
		if (!status?.last_checked_at_unix_ms) return $_("settings.pricing.neverChecked");
		return $_("settings.pricing.lastChecked", { values: { time: new Date(status.last_checked_at_unix_ms).toLocaleString() } });
	}
</script>

<SectionCard title={$_("settings.pricing.title")} description={$_("settings.pricing.description")}>
	<Field.FieldGroup>
		<SettingsRow label={$_("settings.pricing.automatic.label")} description={$_("settings.pricing.automatic.description")} forID="automatic-pricing">
			{#snippet control()}
				{#if busy === "automatic"}<Spinner />{/if}
				<Switch.Root id="automatic-pricing" checked={status?.automatic ?? true} disabled={!status || !!busy} onCheckedChange={setAutomatic} />
			{/snippet}
		</SettingsRow>
		<SettingsRow label={$_("settings.pricing.status")} description={description()}>
			{#snippet control()}
				<Button size="sm" variant="outline" disabled={!status || !!busy} onclick={check}>
					{#if busy === "check"}<Spinner />{/if}
					{$_("settings.pricing.check")}
				</Button>
			{/snippet}
		</SettingsRow>
	</Field.FieldGroup>
</SectionCard>
