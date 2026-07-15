<script lang="ts">
	import { onMount } from "svelte";
	import { Dialogs, Events } from "@wailsio/runtime";
	import { _ } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import DownloadIcon from "@lucide/svelte/icons/download";
	import KeyRoundIcon from "@lucide/svelte/icons/key-round";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import RotateCcwIcon from "@lucide/svelte/icons/rotate-ccw";
	import TrashIcon from "@lucide/svelte/icons/trash";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";
	import UploadIcon from "@lucide/svelte/icons/upload";

	import { BackupService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		BackupSummary,
		KeyStatus,
		RestorePreview,
		RestoreSource,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/appbackup/models";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import SettingsRow from "$lib/components/app/SettingsRow.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { Badge } from "$lib/components/ui/badge";
	import { Button, buttonVariants } from "$lib/components/ui/button";
	import * as Empty from "$lib/components/ui/empty";
	import * as Field from "$lib/components/ui/field";
	import { Spinner } from "$lib/components/ui/spinner";
	import * as Switch from "$lib/components/ui/switch";
	import * as Table from "$lib/components/ui/table";
	import { desktopErrorCode, desktopErrorDetails, desktopErrorMessage, isCancelError } from "$lib/desktop-errors";
	import { currentDesktopLocale, translate } from "$lib/i18n";

	let {
		automaticBackups,
		databaseHealthy,
		onAutomaticBackupsChange,
	}: {
		automaticBackups: boolean;
		databaseHealthy: boolean;
		onAutomaticBackupsChange: (enabled: boolean) => void;
	} = $props();

	let backups = $state<BackupSummary[]>([]);
	let automaticCleanupRequired = $state(false);
	let keyStatus = $state<KeyStatus>({ available: false, recipient: "" });
	let loading = $state(true);
	let busy = $state("");
	let loadError = $state("");
	let automatic = $state(true);
	let restoreOpen = $state(false);
	let restorePreview = $state<RestorePreview | null>(null);
	let restoreSource = $state<RestoreSource | null>(null);
	let deleteBackup = $state<BackupSummary | null>(null);

	$effect(() => {
		if (busy !== "automatic") automatic = automaticBackups;
	});

	onMount(() => {
		void refresh();
		return Events.On("profiledeck:dashboard-updated", (event) => {
			const payload = event.data as { event?: { kind?: string } } | undefined;
			if (payload?.event?.kind === "application-backup-changed") void refresh();
		});
	});

	async function refresh() {
		loading = true;
		try {
			const [list, status] = await Promise.all([BackupService.List(), BackupService.KeyStatus()]);
			backups = list.backups ?? [];
			automaticCleanupRequired = list.automatic_cleanup_required;
			keyStatus = status;
			loadError = "";
		} catch (error) {
			if (!isCancelError(error)) loadError = formatError(error);
		} finally {
			loading = false;
		}
	}

	async function setAutomatic(enabled: boolean) {
		if (busy || !databaseHealthy) return;
		const previous = automatic;
		automatic = enabled;
		busy = "automatic";
		try {
			const settings = await BackupService.SetAutomatic(enabled);
			automatic = settings.automatic_backups;
			onAutomaticBackupsChange(automatic);
		} catch (error) {
			automatic = previous;
			showError(error);
		} finally {
			busy = "";
		}
	}

	async function createBackup() {
		if (busy || !databaseHealthy) return;
		busy = "create";
		try {
			await BackupService.Create();
			await refresh();
			toast.success(translate("settings.backups.notice.createdTitle"), { description: translate("settings.backups.notice.createdDescription") });
		} catch (error) {
			showError(error);
		} finally {
			busy = "";
		}
	}

	async function exportBackup(backup: BackupSummary) {
		if (busy) return;
		let output = "";
		try {
			output = await Dialogs.SaveFile({
				Title: translate("settings.backups.exportDialog"),
				Filename: `${backup.id}.profiledeck-backup`,
				Filters: [{ DisplayName: translate("settings.backups.fileType"), Pattern: "*.profiledeck-backup" }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (!output) return;
		busy = `export:${backup.id}`;
		try {
			await BackupService.Export({ backup_id: backup.id, output_path: output, overwrite: false });
			toast.success(translate("settings.backups.notice.exportedTitle"), { description: translate("settings.backups.notice.exportedDescription") });
		} catch (error) {
			showError(error);
		} finally {
			busy = "";
		}
	}

	async function chooseRestoreFile() {
		if (busy) return;
		let path = "";
		try {
			path = await Dialogs.OpenFile({
				Title: translate("settings.backups.restoreFileDialog"),
				Filters: [{ DisplayName: translate("settings.backups.fileType"), Pattern: "*.profiledeck-backup" }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (path) await previewRestore({ backup_id: "", file_path: path });
	}

	async function previewRestore(source: RestoreSource) {
		if (busy) return;
		busy = "preview-restore";
		try {
			restorePreview = await BackupService.PreviewRestore(source);
			restoreSource = source;
			restoreOpen = true;
		} catch (error) {
			showError(error);
		} finally {
			busy = "";
		}
	}

	async function confirmRestore() {
		if (!restorePreview || !restoreSource || busy) return;
		busy = "restore";
		try {
			await BackupService.Restore({
				source: restoreSource,
				expected_fingerprint: restorePreview.fingerprint,
				confirm: true,
			});
			restoreOpen = false;
		} catch (error) {
			showError(error);
			busy = "";
		}
	}

	async function confirmDelete() {
		if (!deleteBackup || busy) return;
		const backup = deleteBackup;
		busy = `delete:${backup.id}`;
		try {
			await BackupService.Delete({ backup_id: backup.id, confirm: true });
			deleteBackup = null;
			await refresh();
			toast.success(translate("settings.backups.notice.deletedTitle"), { description: translate("settings.backups.notice.deletedDescription") });
		} catch (error) {
			showError(error);
		} finally {
			busy = "";
		}
	}

	async function exportKey() {
		if (busy || !keyStatus.available) return;
		const confirm = translate("settings.backups.key.exportAction");
		let answer = "";
		try {
			answer = await Dialogs.Question({
				Title: translate("settings.backups.key.exportTitle"),
				Message: translate("settings.backups.key.exportWarning"),
				Buttons: [{ Label: confirm }, { Label: translate("actions.cancel"), IsCancel: true, IsDefault: true }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (answer !== confirm) return;
		let output = "";
		try {
			output = await Dialogs.SaveFile({
				Title: translate("settings.backups.key.exportDialog"),
				Filename: "profiledeck-recovery-key.txt",
				Filters: [{ DisplayName: translate("settings.backups.key.fileType"), Pattern: "*.txt" }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (!output) return;
		busy = "export-key";
		try {
			await BackupService.ExportKey({ output_path: output, confirm: true });
			toast.success(translate("settings.backups.notice.keyExportedTitle"), { description: translate("settings.backups.notice.keyExportedDescription") });
		} catch (error) {
			showError(error);
		} finally {
			busy = "";
		}
	}

	async function importKey() {
		if (busy) return;
		let path = "";
		try {
			path = await Dialogs.OpenFile({
				Title: translate("settings.backups.key.importDialog"),
				Filters: [{ DisplayName: translate("settings.backups.key.fileType"), Pattern: "*.txt" }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (!path) return;
		const confirm = translate("settings.backups.key.importAction");
		let answer = "";
		try {
			answer = await Dialogs.Question({
				Title: translate("settings.backups.key.importTitle"),
				Message: translate("settings.backups.key.importWarning"),
				Buttons: [{ Label: confirm }, { Label: translate("actions.cancel"), IsCancel: true, IsDefault: true }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (answer !== confirm) return;
		busy = "import-key";
		try {
			try {
				await BackupService.ImportKey({ input_path: path, replace: false, confirm: true });
			} catch (error) {
				const details = desktopErrorDetails(error);
				if (desktopErrorCode(error) !== "CONFIRMATION_REQUIRED" || details?.reason !== "replace_required") throw error;
				const replace = translate("settings.backups.key.replaceAction");
				const replaceAnswer = await Dialogs.Question({
					Title: translate("settings.backups.key.replaceTitle"),
					Message: translate("settings.backups.key.replaceWarning"),
					Buttons: [{ Label: replace }, { Label: translate("actions.cancel"), IsCancel: true, IsDefault: true }],
				});
				if (replaceAnswer !== replace) return;
				await BackupService.ImportKey({ input_path: path, replace: true, confirm: true });
			}
			await refresh();
			toast.success(translate("settings.backups.notice.keyImportedTitle"), { description: translate("settings.backups.notice.keyImportedDescription") });
		} catch (error) {
			showError(error);
		} finally {
			busy = "";
		}
	}

	function formatError(error: unknown): string {
		return desktopErrorMessage(error, translate("errors.desktopUnavailable"));
	}

	function showError(error: unknown) {
		if (!isCancelError(error)) toast.error(formatError(error));
	}

	function formatDate(value: number): string {
		return new Date(value).toLocaleString(currentDesktopLocale());
	}

	function formatBytes(value: number): string {
		if (!Number.isFinite(value) || value <= 0) return "0 MB";
		return `${(value / (1024 * 1024)).toFixed(value >= 10 * 1024 * 1024 ? 0 : 1)} MB`;
	}
</script>

<SectionCard title={$_("settings.backups.title")} description={$_("settings.backups.description")} contentClass="flex flex-col gap-4">
	<Field.FieldGroup>
		<SettingsRow
			label={$_("settings.backups.automatic.label")}
			description={databaseHealthy ? $_("settings.backups.automatic.description") : $_("settings.backups.automatic.unavailable")}
			forID="desktop-automatic-backups"
			disabled={!databaseHealthy}
		>
			{#snippet control()}
				{#if busy === "automatic"}<Spinner />{/if}
				<Switch.Root id="desktop-automatic-backups" checked={automatic} disabled={!!busy || !databaseHealthy} onCheckedChange={setAutomatic} />
			{/snippet}
		</SettingsRow>

		<SettingsRow label={$_("settings.backups.actions.label")} description={$_("settings.backups.actions.description")}>
			{#snippet control()}
				<Button size="sm" variant="outline" disabled={!!busy} onclick={chooseRestoreFile}>
					<UploadIcon data-icon="inline-start" />{$_("settings.backups.actions.restoreFile")}
				</Button>
				<Button size="sm" disabled={!!busy || !databaseHealthy} onclick={createBackup}>
					{#if busy === "create"}<Spinner data-icon="inline-start" />{:else}<PlusIcon data-icon="inline-start" />{/if}
					{$_("settings.backups.actions.create")}
				</Button>
			{/snippet}
		</SettingsRow>

		<SettingsRow
			label={$_("settings.backups.key.label")}
			description={keyStatus.available ? $_("settings.backups.key.available") : $_("settings.backups.key.missing")}
		>
			{#snippet control()}
				<Button size="sm" variant="outline" disabled={!!busy} onclick={importKey}>
					<KeyRoundIcon data-icon="inline-start" />{$_("settings.backups.key.importAction")}
				</Button>
				<Button size="sm" variant="outline" disabled={!!busy || !keyStatus.available} onclick={exportKey}>
					<DownloadIcon data-icon="inline-start" />{$_("settings.backups.key.exportAction")}
				</Button>
			{/snippet}
		</SettingsRow>
	</Field.FieldGroup>

	{#if loadError}
		<Alert.Root variant="destructive">
			<Alert.Title>{$_("settings.backups.loadFailed")}</Alert.Title>
			<Alert.Description>{loadError}</Alert.Description>
			<Alert.Action><Button size="xs" variant="outline" onclick={refresh}>{$_("actions.retry")}</Button></Alert.Action>
		</Alert.Root>
	{:else if loading}
		<div class="grid min-h-24 place-items-center"><Spinner /></div>
	{:else if backups.length === 0}
		<Empty.Root class="min-h-32 border">
			<Empty.Header>
				<Empty.Title>{$_("settings.backups.emptyTitle")}</Empty.Title>
				<Empty.Description>{$_("settings.backups.emptyDescription")}</Empty.Description>
			</Empty.Header>
		</Empty.Root>
	{:else}
		{#if automaticCleanupRequired}
			<Alert.Root>
				<TriangleAlertIcon />
				<Alert.Title>{$_("settings.backups.cleanupTitle")}</Alert.Title>
				<Alert.Description>{$_("settings.backups.cleanupDescription")}</Alert.Description>
			</Alert.Root>
		{/if}
		<div class="max-h-72 overflow-auto rounded-md border">
			<Table.Root>
				<Table.Header><Table.Row>
					<Table.Head>{$_("settings.backups.table.backup")}</Table.Head>
					<Table.Head>{$_("settings.backups.table.created")}</Table.Head>
					<Table.Head>{$_("settings.backups.table.size")}</Table.Head>
					<Table.Head><span class="sr-only">{$_("actions.actions")}</span></Table.Head>
				</Table.Row></Table.Header>
				<Table.Body>
					{#each backups as backup (backup.id)}
						<Table.Row>
							<Table.Cell><div class="flex flex-col gap-1"><span class="font-mono text-xs">{backup.id}</span><Badge variant="outline" class="w-fit">{$_(`settings.backups.kind.${backup.kind}`)}</Badge></div></Table.Cell>
							<Table.Cell class="text-sm">{formatDate(backup.created_at_unix_ms)}</Table.Cell>
							<Table.Cell class="text-sm">{formatBytes(backup.size_bytes)}</Table.Cell>
							<Table.Cell><div class="flex justify-end gap-1">
								<Button size="icon-sm" variant="ghost" aria-label={$_("settings.backups.actions.export")} disabled={!!busy} onclick={() => exportBackup(backup)}><DownloadIcon /></Button>
								<Button size="icon-sm" variant="ghost" aria-label={$_("settings.backups.actions.restore")} disabled={!!busy} onclick={() => previewRestore({ backup_id: backup.id, file_path: "" })}><RotateCcwIcon /></Button>
								<Button size="icon-sm" variant="ghost" aria-label={$_("settings.backups.actions.delete")} disabled={!!busy} onclick={() => { deleteBackup = backup; }}><TrashIcon /></Button>
							</div></Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
		</div>
	{/if}
</SectionCard>

<AlertDialog.Root bind:open={restoreOpen}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{$_("settings.backups.restore.title")}</AlertDialog.Title>
			<AlertDialog.Description>{$_("settings.backups.restore.description")}</AlertDialog.Description>
		</AlertDialog.Header>
		{#if restorePreview}
			<div class="flex flex-col gap-3 text-sm">
				<div class="rounded-md border p-3"><div class="font-mono text-xs">{restorePreview.backup.id}</div><div class="mt-1 text-muted-foreground">{formatDate(restorePreview.backup.created_at_unix_ms)}</div></div>
				{#if !restorePreview.current_database_healthy}
					<Alert.Root variant="destructive"><Alert.Title>{$_("settings.backups.restore.noSafetyTitle")}</Alert.Title><Alert.Description>{$_("settings.backups.restore.noSafetyDescription")}</Alert.Description></Alert.Root>
				{/if}
				{#if restorePreview.schema_upgrade_required}<p class="text-muted-foreground">{$_("settings.backups.restore.schemaUpgrade")}</p>{/if}
			</div>
		{/if}
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={busy === "restore"}>{$_("actions.cancel")}</AlertDialog.Cancel>
			<AlertDialog.Action disabled={!restorePreview || busy === "restore"} onclick={confirmRestore}>
				{#if busy === "restore"}<Spinner />{/if}{$_("settings.backups.restore.action")}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>

<AlertDialog.Root open={deleteBackup !== null} onOpenChange={(open) => { if (!open) deleteBackup = null; }}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{$_("settings.backups.delete.title")}</AlertDialog.Title>
			<AlertDialog.Description>{$_("settings.backups.delete.description")}</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel>
			<AlertDialog.Action class={buttonVariants({ variant: "destructive" })} onclick={confirmDelete}>{$_("settings.backups.actions.delete")}</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
