<script lang="ts">
	import { _ } from "svelte-i18n";
	import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
	import SaveIcon from "@lucide/svelte/icons/save";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";

	import * as Alert from "$lib/components/ui/alert";
	import * as Breadcrumb from "$lib/components/ui/breadcrumb";
	import * as Card from "$lib/components/ui/card";
	import * as Field from "$lib/components/ui/field";
	import * as RadioGroup from "$lib/components/ui/radio-group";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Spinner } from "$lib/components/ui/spinner";
	import { translate } from "$lib/i18n";

	import type { CodexDetectResult, CodexProfileDetail } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import ProfileForm from "./ProfileForm.svelte";
	import type { CodexForkBinding } from "./types";

	interface Props {
		mode: "new" | "fork";
		detail?: CodexProfileDetail | null;
		detectResult: CodexDetectResult | null;
		canChooseConfigSet?: boolean;
		busy: boolean;
		profileID?: string;
		profileName?: string;
		profileDescription?: string;
		configMode?: "reuse" | "new";
		credentialBinding?: CodexForkBinding;
		configBinding?: CodexForkBinding;
		newConfigSetID?: string;
		newConfigSetName?: string;
		idError: string;
		nameError: string;
		descriptionError: string;
		onCancel: () => void;
		onSubmit: () => void;
		onRetrySource?: () => void;
		onDiagnostics?: () => void;
	}

	let {
		mode,
		detail = null,
		detectResult,
		canChooseConfigSet = true,
		busy,
		profileID = $bindable(""),
		profileName = $bindable(""),
		profileDescription = $bindable(""),
		configMode = $bindable<"reuse" | "new">("reuse"),
		credentialBinding = $bindable<CodexForkBinding>("copy-new"),
		configBinding = $bindable<CodexForkBinding>("share-parent"),
		newConfigSetID = $bindable(""),
		newConfigSetName = $bindable(""),
		idError,
		nameError,
		descriptionError,
		onCancel,
		onSubmit,
		onRetrySource = () => {},
		onDiagnostics = () => {},
	}: Props = $props();

	let sourceReady = $derived(
		!!detectResult?.profiledeck_initialized && detectResult.provider_compatible && detectResult.config_status === "valid" && detectResult.auth_status === "valid",
	);
	$effect(() => {
		if (mode === "new" && !canChooseConfigSet) configMode = "reuse";
	});

	let needsNewConfigSet = $derived(mode === "new" ? canChooseConfigSet && configMode === "new" : configBinding === "copy-new");
	let forkValid = $derived(mode !== "fork" || credentialBinding === "copy-new" || configBinding === "copy-new");
	let submitReady = $derived(!idError && !nameError && !descriptionError && forkValid && (!needsNewConfigSet || (!!newConfigSetID.trim() && !!newConfigSetName.trim())) && (mode === "fork" || sourceReady));
	let title = $derived(mode === "new" ? $_("profilePages.new.title") : $_("profilePages.fork.title", { values: { profile: detail?.summary.profile.name || detail?.summary.profile.id || "" } }));

	function sourceStatusLabel(value: string | undefined): string {
		const status = value === "valid" || value === "invalid" || value === "unreadable" ? value : "missing";
		return translate(`sourceStatus.${status}`);
	}
</script>

<div class="mx-auto flex w-full max-w-4xl flex-col gap-4">
	<Breadcrumb.Root>
		<Breadcrumb.List>
			<Breadcrumb.Item><Breadcrumb.Link href="#/codex/profiles">{$_("tabs.profiles")}</Breadcrumb.Link></Breadcrumb.Item>
			<Breadcrumb.Separator />
			<Breadcrumb.Item><Breadcrumb.Page>{title}</Breadcrumb.Page></Breadcrumb.Item>
		</Breadcrumb.List>
	</Breadcrumb.Root>

	<div class="flex flex-col gap-1">
		<h2 class="text-xl font-semibold tracking-tight">{title}</h2>
		<p class="text-sm text-muted-foreground">{mode === "new" ? $_("profilePages.new.description") : $_("profilePages.fork.description")}</p>
	</div>

	{#if mode === "new"}
		<Card.Root>
			<Card.Header><Card.Title>{$_("profilePages.source.title")}</Card.Title><Card.Description>{$_("profilePages.source.description")}</Card.Description></Card.Header>
			<Card.Content class="flex flex-wrap gap-2">
				<Badge variant={detectResult?.config_status === "valid" ? "secondary" : "destructive"}>config.toml · {sourceStatusLabel(detectResult?.config_status)}</Badge>
				<Badge variant={detectResult?.auth_status === "valid" ? "secondary" : "destructive"}>auth.json · {sourceStatusLabel(detectResult?.auth_status)}</Badge>
				{#if !sourceReady}
					<Alert.Root variant="destructive" class="basis-full">
						<Alert.Description>{$_("profilePages.source.statusDescription", { values: { config: sourceStatusLabel(detectResult?.config_status), auth: sourceStatusLabel(detectResult?.auth_status) } })}</Alert.Description>
						<Alert.Action>
							<div class="flex gap-2">
								<Button size="xs" variant="outline" onclick={onRetrySource}><RefreshCwIcon />{$_("actions.retry")}</Button>
								<Button size="xs" variant="outline" onclick={onDiagnostics}>{$_("nav.diagnostics")}</Button>
							</div>
						</Alert.Action>
					</Alert.Root>
				{/if}
			</Card.Content>
		</Card.Root>
	{/if}

	<Card.Root>
		<Card.Header><Card.Title>{$_("profilePages.form.profile")}</Card.Title><Card.Description>{$_("profilePages.form.profileDescription")}</Card.Description></Card.Header>
		<Card.Content><ProfileForm bind:profileID bind:name={profileName} bind:description={profileDescription} {idError} {nameError} {descriptionError} /></Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header><Card.Title>{$_("profilePages.configChoice.title")}</Card.Title><Card.Description>{$_(mode === "new" && !canChooseConfigSet ? "profilePages.configChoice.firstDescription" : "profilePages.configChoice.description")}</Card.Description></Card.Header>
		<Card.Content class="flex flex-col gap-4">
			{#if mode === "new" && canChooseConfigSet}
				<RadioGroup.Root bind:value={configMode} class="grid gap-3 md:grid-cols-2">
					<Field.FieldLabel class="items-start rounded-lg border p-3">
						<RadioGroup.Item value="reuse" /><Field.FieldContent><Field.FieldTitle>{$_("profilePages.configChoice.reuse")}</Field.FieldTitle><Field.FieldDescription>{$_("profilePages.configChoice.reuseDescription")}</Field.FieldDescription></Field.FieldContent>
					</Field.FieldLabel>
					<Field.FieldLabel class="items-start rounded-lg border p-3">
						<RadioGroup.Item value="new" /><Field.FieldContent><Field.FieldTitle>{$_("profilePages.configChoice.new")}</Field.FieldTitle><Field.FieldDescription>{$_("profilePages.configChoice.newDescription")}</Field.FieldDescription></Field.FieldContent>
					</Field.FieldLabel>
				</RadioGroup.Root>
			{:else if mode === "new"}
				<div class="flex items-center gap-2 text-sm"><Badge variant="outline">shared</Badge><span class="text-muted-foreground">{$_("profilePages.configChoice.codexSettings")}</span></div>
			{:else}
				<div class="grid gap-4 md:grid-cols-2">
					<Field.FieldSet><Field.FieldLegend>{$_("profilePages.fork.credentialBinding")}</Field.FieldLegend><RadioGroup.Root bind:value={credentialBinding} class="flex flex-col gap-2"><Field.FieldLabel><RadioGroup.Item value="share-parent" />{$_("profilePages.fork.shareParent")}</Field.FieldLabel><Field.FieldLabel><RadioGroup.Item value="copy-new" />{$_("profilePages.fork.copyNew")}</Field.FieldLabel></RadioGroup.Root></Field.FieldSet>
					<Field.FieldSet><Field.FieldLegend>{$_("profilePages.fork.configBinding")}</Field.FieldLegend><RadioGroup.Root bind:value={configBinding} class="flex flex-col gap-2"><Field.FieldLabel><RadioGroup.Item value="share-parent" />{$_("profilePages.fork.shareConfig")}</Field.FieldLabel><Field.FieldLabel><RadioGroup.Item value="copy-new" />{$_("profilePages.fork.copyConfig")}</Field.FieldLabel></RadioGroup.Root></Field.FieldSet>
				</div>
				{#if !forkValid}<Alert.Root variant="destructive"><Alert.Description>{$_("profilePages.fork.copyRequired")}</Alert.Description></Alert.Root>{/if}
			{/if}

			{#if needsNewConfigSet}
				<Field.FieldGroup>
					<Field.Field><Field.FieldLabel for="new-config-set-id">{$_("configSets.id")}</Field.FieldLabel><Input id="new-config-set-id" bind:value={newConfigSetID} autocomplete="off" spellcheck="false" /></Field.Field>
					<Field.Field><Field.FieldLabel for="new-config-set-name">{$_("configSets.name")}</Field.FieldLabel><Input id="new-config-set-name" bind:value={newConfigSetName} maxlength={120} /></Field.Field>
				</Field.FieldGroup>
			{/if}
		</Card.Content>
		<Card.Footer class="justify-end gap-2">
			<Button variant="outline" onclick={onCancel}><ArrowLeftIcon data-icon="inline-start" />{$_("actions.cancel")}</Button>
			<Button disabled={!submitReady || busy} onclick={onSubmit}>{#if busy}<Spinner data-icon="inline-start" />{:else}<SaveIcon data-icon="inline-start" />{/if}{mode === "new" ? $_("actions.saveAsNewProfile") : $_("actions.fork")}</Button>
		</Card.Footer>
	</Card.Root>
</div>
