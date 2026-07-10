<script lang="ts">
	import { _ } from "svelte-i18n";

	import * as Dialog from "$lib/components/ui/dialog";
	import * as Field from "$lib/components/ui/field";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Spinner } from "$lib/components/ui/spinner";
	import { Textarea } from "$lib/components/ui/textarea";

	import type { ConfigSetDialogMode } from "./types";

	interface Props {
		open?: boolean;
		mode: ConfigSetDialogMode;
		busy: boolean;
		configSetID?: string;
		name?: string;
		description?: string;
		onClose: () => void;
		onSubmit: (value: { id: string; name: string; description: string }) => void;
	}

	let { open = $bindable(false), mode, busy, configSetID = "", name = "", description = "", onClose, onSubmit }: Props = $props();
	let id = $state("");
	let displayName = $state("");
	let details = $state("");

	$effect(() => {
		if (!open) return;
		id = mode === "edit" ? configSetID : mode === "copy" ? `${configSetID}-copy` : "";
		displayName = mode === "copy" ? `${name} copy` : name;
		details = description;
	});

	let valid = $derived(!!id.trim() && !!displayName.trim());
</script>

<Dialog.Root {open} onOpenChange={(value) => { open = value; if (!value) onClose(); }}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>{$_(`configSets.dialog.${mode}Title`)}</Dialog.Title>
			<Dialog.Description>{$_(`configSets.dialog.${mode}Description`)}</Dialog.Description>
		</Dialog.Header>
		<Field.FieldGroup>
			<Field.Field data-disabled={mode === "edit"}>
				<Field.FieldLabel for="config-set-id">{$_("configSets.id")}</Field.FieldLabel>
				<Input id="config-set-id" bind:value={id} disabled={mode === "edit"} autocomplete="off" spellcheck="false" />
			</Field.Field>
			<Field.Field>
				<Field.FieldLabel for="config-set-name">{$_("configSets.name")}</Field.FieldLabel>
				<Input id="config-set-name" bind:value={displayName} maxlength={120} />
			</Field.Field>
			<Field.Field>
				<Field.FieldLabel for="config-set-description">{$_("configSets.descriptionLabel")}</Field.FieldLabel>
				<Textarea id="config-set-description" bind:value={details} rows={3} maxlength={1000} />
			</Field.Field>
		</Field.FieldGroup>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => { open = false; onClose(); }}>{$_("actions.cancel")}</Button>
			<Button disabled={!valid || busy} onclick={() => onSubmit({ id: id.trim(), name: displayName.trim(), description: details.trim() })}>
				{#if busy}<Spinner data-icon="inline-start" />{/if}{$_("actions.save")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
