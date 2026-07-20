<script lang="ts">
	import { _ } from "svelte-i18n";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import { ProfileService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { Button } from "$lib/components/ui/button";
	import { Spinner } from "$lib/components/ui/spinner";
	import { desktopErrorDetails, desktopErrorMessage, isCancelError } from "$lib/desktop-errors";
	import { translate } from "$lib/i18n";

	export type ProfileDeleteTarget = {
		id: string;
		name: string;
	};

	interface Props {
		open?: boolean;
		profile: ProfileDeleteTarget | null;
		onDeleted: (profile: ProfileDeleteTarget) => void;
	}

	let { open = $bindable(false), profile, onDeleted }: Props = $props();
	let deleting = $state(false);
	let inlineError = $state("");
	let dialogKey = "";

	$effect(() => {
		const nextKey = open && profile ? profile.id : "";
		if (nextKey === dialogKey) return;
		dialogKey = nextKey;
		inlineError = "";
	});

	async function confirmDelete() {
		if (!profile || deleting) return;
		const target = profile;
		deleting = true;
		inlineError = "";
		try {
			await ProfileService.Delete(target.id, true);
			open = false;
			onDeleted(target);
		} catch (error) {
			if (!isCancelError(error)) inlineError = deleteErrorMessage(error);
		} finally {
			deleting = false;
		}
	}

	function deleteErrorMessage(error: unknown): string {
		const reason = desktopErrorDetails(error)?.reason;
		if (reason === "active") return translate("profileDelete.activeError");
		if (reason === "unresolved_operation") return translate("profileDelete.unresolvedError");
		if (reason === "unsupported_managed_data") return translate("profileDelete.unsupportedError");
		return desktopErrorMessage(error, translate("profileDelete.genericError"));
	}
</script>

<AlertDialog.Root {open} onOpenChange={(value) => { if (!deleting || value) open = value; }}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{$_("profileDelete.title", { values: { name: profile?.name || $_("profile.unnamed") } })}</AlertDialog.Title>
			<AlertDialog.Description>{$_("profileDelete.description")}</AlertDialog.Description>
			{#if profile}<p class="break-all font-mono text-xs text-muted-foreground">{$_("profileDelete.identity", { values: { id: profile.id } })}</p>{/if}
		</AlertDialog.Header>
		{#if inlineError}
			<Alert.Root variant="destructive">
				<TriangleAlertIcon data-icon="inline-start" />
				<Alert.Title>{$_("profileDelete.errorTitle")}</Alert.Title>
				<Alert.Description>{inlineError}</Alert.Description>
			</Alert.Root>
		{/if}
		<AlertDialog.Footer>
			<Button variant="outline" disabled={deleting} onclick={() => (open = false)}>{$_("actions.cancel")}</Button>
			<AlertDialog.Action
				variant="destructive"
				disabled={!profile || deleting}
				onclick={(event) => {
					event.preventDefault();
					void confirmDelete();
				}}
			>
				{#if deleting}<Spinner data-icon="inline-start" />{/if}
				{$_("actions.deleteProfile")}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
