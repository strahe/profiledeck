<script lang="ts">
	import { _ } from "svelte-i18n";

	import * as Field from "$lib/components/ui/field";
	import { Input } from "$lib/components/ui/input";
	import { Textarea } from "$lib/components/ui/textarea";

	let {
		profileID = $bindable(""),
		name = $bindable(""),
		description = $bindable(""),
		idLocked = false,
		idError = "",
		nameError = "",
		descriptionError = "",
	}: {
		profileID?: string;
		name?: string;
		description?: string;
		idLocked?: boolean;
		idError?: string;
		nameError?: string;
		descriptionError?: string;
	} = $props();
</script>

<Field.FieldGroup>
	<Field.Field data-invalid={!!idError} data-disabled={idLocked}>
		<Field.FieldLabel for="profile-id">{$_("profilePages.form.profileID")}</Field.FieldLabel>
		<Input
			id="profile-id"
			bind:value={profileID}
			disabled={idLocked}
			aria-invalid={!!idError}
			maxlength={80}
			autocapitalize="none"
			autocomplete="off"
			spellcheck="false"
			placeholder={$_("profilePages.form.profileIDPlaceholder")}
		/>
		<Field.FieldDescription>{$_("profilePages.form.profileIDHelp")}</Field.FieldDescription>
		<Field.FieldError errors={idError ? [{ message: idError }] : []} />
	</Field.Field>
	<Field.FieldSeparator />

	<Field.Field data-invalid={!!nameError}>
		<Field.FieldLabel for="profile-name">{$_("profilePages.form.name")}</Field.FieldLabel>
		<Input
			id="profile-name"
			bind:value={name}
			aria-invalid={!!nameError}
			maxlength={120}
			autocapitalize="none"
			autocomplete="off"
			placeholder={$_("profilePages.form.namePlaceholder")}
		/>
		<Field.FieldDescription>{$_("profilePages.form.nameHelp")}</Field.FieldDescription>
		<Field.FieldError errors={nameError ? [{ message: nameError }] : []} />
	</Field.Field>
	<Field.FieldSeparator />

	<Field.Field data-invalid={!!descriptionError}>
		<Field.FieldLabel for="profile-description">{$_("profilePages.form.description")}</Field.FieldLabel>
		<Textarea
			id="profile-description"
			bind:value={description}
			aria-invalid={!!descriptionError}
			maxlength={1000}
			rows={3}
			placeholder={$_("profilePages.form.descriptionPlaceholder")}
		/>
		<Field.FieldDescription>{$_("profilePages.form.descriptionHelp")}</Field.FieldDescription>
		<Field.FieldError errors={descriptionError ? [{ message: descriptionError }] : []} />
	</Field.Field>
</Field.FieldGroup>
