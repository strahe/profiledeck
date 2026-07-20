<script lang="ts">
	import { onMount, tick } from "svelte";
	import { Dialogs, type CancellablePromise } from "@wailsio/runtime";
	import { push } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import DownloadIcon from "@lucide/svelte/icons/download";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import SlidersHorizontalIcon from "@lucide/svelte/icons/sliders-horizontal";
	import UploadIcon from "@lucide/svelte/icons/upload";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import * as Tooltip from "$lib/components/ui/tooltip";

	import { CodexService, SwitchService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		ApplyCodexProfileImportRequest,
		CopyCodexConfigSetRequest,
		CreateCodexConfigSetRequest,
		CreateCodexProfileRequest,
		ExportCodexProfilesRequest,
		ForkCodexProfileRequest,
		UpdateCodexConfigSetRequest,
		UpdateCodexProfileMetadataRequest,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexConfigSet,
		CodexDetectResult,
		CodexProfileDetail,
		CodexProfileExportResult,
		CodexProfileImportPlan,
		CodexProfileSaveResult,
		CodexProfileSummary,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/codex/models";
	import type { SwitchPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/switching/models";

	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import * as Dialog from "$lib/components/ui/dialog";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import * as Field from "$lib/components/ui/field";
	import * as Select from "$lib/components/ui/select";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { Textarea } from "$lib/components/ui/textarea";
	import { desktopErrorDetails, desktopErrorMessage, isCancelError, isDesktopErrorCode } from "$lib/desktop-errors";
	import { currentDesktopLocale, translate } from "$lib/i18n";
	import { joinUserMessages, profileChangeWarningMessage } from "$lib/user-facing-messages";

	import ConfigSetDialog from "./ConfigSetDialog.svelte";
	import ConfigSetPage from "./ConfigSetPage.svelte";
	import ProfileDetail from "./ProfileDetail.svelte";
	import ProfileDeleteDialog, { type ProfileDeleteTarget } from "./ProfileDeleteDialog.svelte";
	import ProfileEditorPage from "./ProfileEditorPage.svelte";
	import ProfileImportDialog from "./ProfileImportDialog.svelte";
	import ProfileList from "./ProfileList.svelte";
	import UseProfileDialog from "./UseProfileDialog.svelte";
	import type { CodexForkBinding, CodexProfileListItem, CodexProfileRoute, ConfigSetDialogState, ProfileUseRequest } from "./types";
	import { useCodexRuntime } from "../settings/codex-runtime.svelte.js";

	interface Props {
		route: CodexProfileRoute;
		profiles: CodexProfileSummary[];
		dashboardConfigSets: CodexConfigSet[];
		detectResult: CodexDetectResult | null;
		detectError: string;
		activeProfileID: string;
		loadingProfiles: boolean;
		profileError: string;
		useRequest: ProfileUseRequest | null;
		refreshDetect: () => Promise<CodexDetectResult | null>;
		refreshProfiles: () => Promise<void>;
		cancelDetect: () => void;
		onUseRequestHandled: (sequence: number) => void;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	}

	let { route, profiles, dashboardConfigSets, detectResult, detectError, activeProfileID, loadingProfiles, profileError, useRequest, refreshDetect, refreshProfiles, cancelDetect, onUseRequestHandled, showError, showNotice }: Props = $props();

	const codexProviderID = "codex";
	const inFlight = new Map<string, CancellablePromise<unknown>>();
	const runtime = useCodexRuntime();

	let busyAction = $state("");
	let detail = $state<CodexProfileDetail | null>(null);
	let detailLoading = $state(false);
	let detailError = $state("");
	let routeKey = "";
	let routeSequence = 0;

	let profileID = $state("");
	let profileName = $state("");
	let profileDescription = $state("");
	let formSubmitted = $state(false);
	let configMode = $state<"reuse" | "new">("reuse");
	let credentialBinding = $state<CodexForkBinding>("copy-new");
	let configBinding = $state<CodexForkBinding>("share-parent");
	let newConfigSetID = $state("");
	let newConfigSetName = $state("");

	let configSets = $state<CodexConfigSet[]>([]);
	let configSetsLoading = $state(false);
	let configSetsError = $state("");
	let configDialogOpen = $state(false);
	let configDialog = $state<ConfigSetDialogState>({ mode: "create", source: null });

	let editOpen = $state(false);
	let editName = $state("");
	let editDescription = $state("");
	let saveCurrentOpen = $state(false);
	let saveCurrentSourceError = $state("");
	let setConfigOpen = $state(false);
	let selectedConfigSetID = $state("");
	let importOpen = $state(false);
	let importPath = $state("");
	let importPlan = $state<CodexProfileImportPlan | null>(null);
	let deleteOpen = $state(false);
	let deleteTarget = $state<ProfileDeleteTarget | null>(null);

	let useOpen = $state(false);
	let useProfile = $state<CodexProfileListItem | null>(null);
	let usePlan = $state<SwitchPlan | null>(null);
	let useBuilding = $state(false);
	let useApplying = $state(false);
	let useInlineError = $state("");
	let useSequence = 0;
	let handledUseSequence = 0;

	let listItems = $derived.by(() => {
		void $locale;
		return [...profiles]
			.sort((left, right) => Number(right.active) - Number(left.active))
			.map(profileListItem);
	});
	let sourceReady = $derived(isSourceReady(detectResult));
	let rawIDError = $derived.by(() => { void $locale; return validateProfileID(profileID); });
	let rawNameError = $derived.by(() => { void $locale; return validateOptionalName(profileName); });
	let rawDescriptionError = $derived.by(() => { void $locale; return validateDescription(profileDescription); });
	let displayedIDError = $derived(formSubmitted || profileID ? rawIDError : "");
	let displayedNameError = $derived(formSubmitted || profileName ? rawNameError : "");
	let displayedDescriptionError = $derived(formSubmitted || profileDescription ? rawDescriptionError : "");

	$effect(() => {
		configSets = dashboardConfigSets;
	});

	$effect(() => {
		const key = `${route.kind}:${route.profileID}`;
		if (key === routeKey) return;
		routeKey = key;
		void enterRoute(route);
	});

	$effect(() => {
		if (!useRequest || useRequest.sequence === handledUseSequence) return;
		handledUseSequence = useRequest.sequence;
		void openUseByProfileID(useRequest.profileID).finally(() => onUseRequestHandled(useRequest.sequence));
	});

	onMount(() => {
		return () => {
			cancelAll();
			cancelDetect();
		};
	});

	async function enterRoute(next: CodexProfileRoute) {
		const sequence = ++routeSequence;
		cancelAll();
		closeUse();
		busyAction = "";
		detailError = "";
		formSubmitted = false;
		editOpen = false;
		setConfigOpen = false;
		if (next.kind === "list") {
			detail = null;
			resetForm();
			await refreshDetect();
			return;
		}
		if (next.kind === "config-sets") {
			detail = null;
			await refreshConfigSets();
			return;
		}
		if (next.kind === "new") {
			detail = null;
			resetForm();
			await refreshDetect();
			return;
		}
		await loadDetail(next.profileID, next.kind === "fork", sequence);
	}

	async function loadDetail(id: string, prepareFork = false, sequence = routeSequence) {
		detailLoading = true;
		try {
			const value = await track("profile-detail", CodexService.ShowProfile(id));
			if (sequence !== routeSequence) return;
			detail = value;
			if (prepareFork) {
				profileID = `${value.summary.profile.id}-copy`;
				profileName = translate("profilePages.fork.copyName", { profile: value.summary.profile.name || value.summary.profile.id });
				profileDescription = value.summary.profile.description || "";
				credentialBinding = "copy-new";
				configBinding = "share-parent";
				newConfigSetID = `${value.summary.profile.id}-config-copy`;
				newConfigSetName = `${value.config_set?.name || value.summary.profile.name || value.summary.profile.id} copy`;
			}
		} catch (error) {
			if (sequence === routeSequence && !isCancelError(error)) detailError = formatError(error);
		} finally {
			if (sequence === routeSequence) detailLoading = false;
		}
	}

	function resetForm() {
		profileID = "";
		profileName = "";
		profileDescription = "";
		configMode = "reuse";
		credentialBinding = "copy-new";
		configBinding = "share-parent";
		newConfigSetID = "";
		newConfigSetName = "";
	}

	async function createProfile() {
		formSubmitted = true;
		if (rawIDError || rawNameError || rawDescriptionError) return;
		const request: CreateCodexProfileRequest = {
			profile_id: profileID.trim(), name: optional(profileName), description: optional(profileDescription),
			new_config_set_id: configMode === "new" ? newConfigSetID.trim() : "",
			new_config_set_name: configMode === "new" ? optional(newConfigSetName) : null,
		};
		await runAction("profile-create", async () => {
			if (!isSourceReady(await refreshDetect())) return;
			const result = await track("profile-create", CodexService.CreateProfile(request));
			await refreshProfiles();
			showResultWarnings(result);
			showNotice(translate("notice.profileCreated.title"), translate("notice.profileCreated.codexDescription", { profile: result.profile.name || result.profile.id }));
			await push(`/codex/profiles/${encodeURIComponent(result.profile.id)}`);
		});
	}

	async function forkProfile() {
		formSubmitted = true;
		if (!detail || rawIDError || rawNameError || rawDescriptionError) return;
		const request: ForkCodexProfileRequest = {
			source_profile_id: detail.summary.profile.id, profile_id: profileID.trim(),
			credential_binding: credentialBinding, config_binding: configBinding,
			new_config_set_id: configBinding === "copy-new" ? newConfigSetID.trim() : "",
			new_config_set_name: configBinding === "copy-new" ? optional(newConfigSetName) : null,
			name: optional(profileName), description: optional(profileDescription),
		};
		await runAction("profile-fork", async () => {
			const result = await track("profile-fork", CodexService.ForkProfile(request));
			await refreshProfiles();
			showResultWarnings(result);
			showNotice(translate("notice.profileForked.title"), translate("notice.profileForked.codexDescription", { profile: result.profile.name || result.profile.id }));
			await push(`/codex/profiles/${encodeURIComponent(result.profile.id)}`);
		});
	}

	function openEdit() {
		if (!detail) return;
		editName = detail.summary.profile.name || detail.summary.profile.id;
		editDescription = detail.summary.profile.description || "";
		editOpen = true;
	}

	function openProfileDelete(profile: { id: string; name: string }) {
		deleteTarget = { id: profile.id, name: profile.name || translate("profile.unnamed") };
		deleteOpen = true;
	}

	function profileDeleted(profile: ProfileDeleteTarget) {
		if (detail?.summary.profile.id === profile.id) {
			detail = null;
			void push("/codex/profiles");
		}
		void refreshProfiles();
		showNotice(translate("profileDelete.deletedTitle"), translate("profileDelete.deletedDescription"));
	}

	async function saveMetadata() {
		if (!detail || !editName.trim()) return;
		const request: UpdateCodexProfileMetadataRequest = { profile_id: detail.summary.profile.id, name: editName.trim(), description: optional(editDescription) };
		await runAction("profile-metadata", async () => {
			await track("profile-metadata", CodexService.UpdateProfileMetadata(request));
			editOpen = false;
			await Promise.all([refreshProfiles(), loadDetail(detail!.summary.profile.id)]);
			showNotice(translate("notice.profileUpdated.title"), translate("notice.profileUpdated.description"));
		});
	}

	async function saveCurrent() {
		await runAction("profile-save-current", async () => {
			const source = await refreshDetect();
			if (!isSourceReady(source)) {
				saveCurrentSourceError = sourceStatusDescription(source);
				return;
			}
			saveCurrentSourceError = "";
			const result = await track("profile-save-current", CodexService.SaveActiveProfileState());
			saveCurrentOpen = false;
			await Promise.all([refreshProfiles(), refreshConfigSets(), detail ? loadDetail(detail.summary.profile.id) : Promise.resolve()]);
			if (result.warnings?.length) toast.warning(translate("notice.profileWarnings.title"), { description: joinUserMessages(result.warnings, profileChangeWarningMessage) });
			showNotice(translate("notice.profileSaved.title"), translate("notice.profileSaved.description"));
		});
	}

	function openSaveCurrent() {
		saveCurrentSourceError = "";
		saveCurrentOpen = true;
	}

	async function openSetConfig() {
		await refreshConfigSets();
		selectedConfigSetID = detail?.summary.config_set_id || configSets[0]?.id || "";
		setConfigOpen = true;
	}

	async function setProfileConfig() {
		if (!detail || !selectedConfigSetID) return;
		await runAction("profile-set-config", async () => {
			await track("profile-set-config", CodexService.SetProfileConfig({ profile_id: detail!.summary.profile.id, config_set_id: selectedConfigSetID }));
			setConfigOpen = false;
			await Promise.all([refreshProfiles(), loadDetail(detail!.summary.profile.id), refreshConfigSets()]);
		});
	}

	async function refreshConfigSets() {
		configSetsLoading = true;
		try {
			const result = await track("config-sets", CodexService.ListConfigSets());
			configSets = result.config_sets ?? [];
			configSetsError = "";
		} catch (error) {
			if (!isCancelError(error)) configSetsError = formatError(error);
		} finally {
			configSetsLoading = false;
		}
	}

	function openConfigDialog(mode: ConfigSetDialogState["mode"], source: CodexConfigSet | null = null) {
		configDialog = { mode, source };
		configDialogOpen = true;
	}

	async function submitConfigDialog(value: { id: string; name: string; description: string }) {
		await runAction("config-set-save", async () => {
			if (configDialog.mode === "create") {
				const request: CreateCodexConfigSetRequest = { config_set_id: value.id, name: value.name, description: value.description };
				await track("config-set-save", CodexService.CreateConfigSet(request));
			} else if (configDialog.mode === "copy" && configDialog.source) {
				const request: CopyCodexConfigSetRequest = { source_config_set_id: configDialog.source.id, config_set_id: value.id, name: value.name, description: value.description };
				await track("config-set-save", CodexService.CopyConfigSet(request));
			} else if (configDialog.source) {
				const request: UpdateCodexConfigSetRequest = { config_set_id: configDialog.source.id, name: value.name, description: value.description };
				await track("config-set-save", CodexService.UpdateConfigSet(request));
			}
			configDialogOpen = false;
			await Promise.all([refreshConfigSets(), refreshProfiles()]);
		});
	}

	async function deleteConfigSet(configSet: CodexConfigSet) {
		await runAction("config-set-delete", async () => {
			await track("config-set-delete", CodexService.DeleteConfigSet(configSet.id));
			await refreshConfigSets();
		});
	}

	async function exportProfiles(profileIDs: string[] = []) {
		let path = "";
		try {
			path = await Dialogs.SaveFile({
				Title: translate("profileTransfer.export.dialogTitle"),
				Filename: profileIDs.length === 1 ? `profiledeck-codex-${profileIDs[0]}.json` : "profiledeck-codex-profiles.json",
				Filters: [{ DisplayName: translate("profileTransfer.fileType"), Pattern: "*.json" }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (!path) return;
		await runAction("profile-export", async () => {
			const request: ExportCodexProfilesRequest = { profile_ids: profileIDs, output_path: path, overwrite: false };
			let result: CodexProfileExportResult;
			try {
				result = await track("profile-export", CodexService.ExportProfiles(request));
			} catch (error) {
				const details = desktopErrorDetails(error);
				if (!isDesktopErrorCode(error, "EXPORT_FAILED") || details?.reason !== "exists") throw error;
				const overwrite = translate("actions.overwrite");
				const answer = await Dialogs.Question({
					Title: translate("profileTransfer.export.overwriteTitle"),
					Message: translate("profileTransfer.export.overwriteDescription"),
					Buttons: [{ Label: overwrite }, { Label: translate("actions.cancel"), IsCancel: true, IsDefault: true }],
				});
				if (answer !== overwrite) return;
				result = await track("profile-export", CodexService.ExportProfiles({ ...request, overwrite: true }));
			}
			showNotice(translate("notice.profilesExported.title"), translate("notice.profilesExported.description", { count: result.profile_count, path: result.path }));
		});
	}

	async function chooseProfileImport() {
		let path = "";
		try {
			path = await Dialogs.OpenFile({
				Title: translate("profileTransfer.import.dialogTitle"),
				Filters: [{ DisplayName: translate("profileTransfer.fileType"), Pattern: "*.json" }],
			});
		} catch (error) {
			if (!isCancelError(error)) showError(error);
			return;
		}
		if (!path) return;
		await runAction("profile-import-inspect", async () => {
			importPlan = await track("profile-import-inspect", CodexService.InspectProfileImport(path));
			importPath = path;
			importOpen = true;
		});
	}

	async function applyProfileImport() {
		if (!importPlan?.can_apply || !importPath || busyAction) return;
		busyAction = "profile-import-apply";
		try {
			const request: ApplyCodexProfileImportRequest = {
				input_path: importPath,
				expected_plan_fingerprint: importPlan.plan_fingerprint,
				confirm: true,
			};
			const result = await track("profile-import-apply", CodexService.ApplyProfileImport(request));
			importOpen = false;
			importPath = "";
			importPlan = null;
			await Promise.all([refreshProfiles(), refreshConfigSets()]);
			showNotice(translate("notice.profilesImported.title"), translate("notice.profilesImported.description", { count: result.profile_count }));
		} catch (error) {
			if (isCancelError(error)) return;
			if (isDesktopErrorCode(error, "IMPORT_PLAN_CHANGED")) {
				try {
					importPlan = await track("profile-import-inspect", CodexService.InspectProfileImport(importPath));
					toast.warning(translate("profileTransfer.import.changedTitle"), { description: translate("profileTransfer.import.changedDescription") });
				} catch (refreshError) {
					if (!isCancelError(refreshError)) showError(refreshError);
				}
			} else {
				showError(error);
			}
		} finally {
			if (busyAction === "profile-import-apply") busyAction = "";
		}
	}

	function closeProfileImport() {
		cancelAction("profile-import-inspect");
		cancelAction("profile-import-apply");
		importOpen = false;
		importPath = "";
		importPlan = null;
	}

	async function openUse(profile: CodexProfileListItem) {
		closeUse();
		const sequence = useSequence;
		useProfile = profile;
		useOpen = true;
		useBuilding = true;
		try {
			const plan = await track("use-build", SwitchService.BuildPlan(codexProviderID, profile.id));
			if (sequence === useSequence) usePlan = plan;
		} catch (error) {
			if (sequence === useSequence && !isCancelError(error)) useInlineError = formatError(error);
		} finally {
			if (sequence === useSequence) useBuilding = false;
		}
	}

	async function openUseByProfileID(id: string) {
		let profile = listItems.find((item) => item.id === id);
		if (!profile) {
			await refreshProfiles();
			await tick();
			profile = listItems.find((item) => item.id === id);
		}
		if (profile) await openUse(profile);
		else showError({ code: "PROFILE_NOT_FOUND" });
	}

	async function confirmUse() {
		if (useApplying || !useProfile || !usePlan?.plan_fingerprint) return;
		const sequence = useSequence;
		useApplying = true;
		try {
			const result = await track("use-apply", SwitchService.Apply({ provider_id: codexProviderID, profile_id: useProfile.id, expected_plan_fingerprint: usePlan.plan_fingerprint, confirm: true }));
			if (sequence !== useSequence) return;
			closeUse();
			await Promise.all([refreshProfiles(), refreshConfigSets()]);
			if (!result.recovery_cleanup_completed) {
				toast.warning(translate("notice.recoveryCleanup.switchAppliedTitle"), {
					description: translate("notice.recoveryCleanup.switchAppliedDescription", { profile: result.profile.name || result.profile.id }),
				});
			} else {
				showNotice(translate("notice.profileSwitched.title"), translate("notice.profileSwitched.codexDescription", { profile: result.profile.name || result.profile.id }));
			}
		} catch (error) {
			if (sequence !== useSequence || isCancelError(error)) return;
			if (isDesktopErrorCode(error, "TARGET_CHANGED")) {
				useInlineError = translate("errors.targetChanged");
				usePlan = null;
				try {
					usePlan = await track("use-build", SwitchService.BuildPlan(codexProviderID, useProfile.id));
				} catch (rebuildError) {
					if (sequence !== useSequence || isCancelError(rebuildError)) return;
					useInlineError = formatError(rebuildError);
					showError(rebuildError);
				}
			} else {
				useInlineError = formatError(error);
				showError(error);
			}
		} finally {
			if (sequence === useSequence) useApplying = false;
		}
	}

	function closeUse() {
		useSequence += 1;
		cancelAction("use-build");
		cancelAction("use-apply");
		useOpen = false;
		useProfile = null;
		usePlan = null;
		useBuilding = false;
		useApplying = false;
		useInlineError = "";
	}

	async function runAction(name: string, action: () => Promise<void>) {
		if (busyAction) return;
		busyAction = name;
		try { await action(); } catch (error) { if (!isCancelError(error)) showError(error); } finally { if (busyAction === name) busyAction = ""; }
	}

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		cancelAction(key);
		inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => { if (inFlight.get(key) === promise) inFlight.delete(key); }).catch(() => {});
		return promise;
	}

	function cancelAction(key: string) { inFlight.get(key)?.cancel("replaced"); inFlight.delete(key); }
	function cancelAll() { for (const promise of inFlight.values()) promise.cancel("route-change"); inFlight.clear(); }
	function showResultWarnings(result: CodexProfileSaveResult) { if (result.warnings?.length) toast.warning(translate("notice.profileWarnings.title"), { description: joinUserMessages(result.warnings, profileChangeWarningMessage) }); }

	function profileListItem(summary: CodexProfileSummary): CodexProfileListItem {
		const quota = runtime.quotaForSummary(summary);
		const quotaRuntime = runtime.runtimeProfile(summary.profile.id);
		const completedAt = quotaRuntime?.last_task === "quota" ? quotaRuntime.last_completed_at_unix_ms : 0;
		const checkedAt = quota?.status === "available"
			? quota.snapshot?.fetched_at_unix_ms || completedAt
			: completedAt;
		const outcome = !checkedAt
			? "never"
			: quota?.status === "available"
				? "updated"
				: quota?.status === "unsupported"
					? "checked"
					: "failed";
		return {
			summary,
			id: summary.profile.id,
			name: summary.profile.name || translate("profile.unnamed"),
			description: summary.profile.description || "",
			quota,
			quotaLoading: runtime.isQuotaLoading(summary.profile.id),
			quotaCheckedAtUnixMS: checkedAt,
			quotaCheckOutcome: outcome,
		};
	}
	function formatRelativeTime(value: number | undefined): string {
		if (!value) return "—";
		const delta = Date.now() - value;
		if (delta < 60_000) return translate("time.justNow");
		if (delta < 3_600_000) return translate("time.minutesAgo", { count: Math.max(1, Math.floor(delta / 60_000)) });
		return new Date(value).toLocaleDateString(currentDesktopLocale(), { month: "short", day: "numeric" });
	}
	function sourceStatusDescription(value: CodexDetectResult | null = detectResult): string {
		if (!value && detectError) return detectError;
		return translate("profilePages.source.statusDescription", {
			config: translate(`sourceStatus.${value?.config_status || "missing"}`),
			auth: translate(`sourceStatus.${value?.auth_status || "missing"}`),
		});
	}
	function isSourceReady(value: CodexDetectResult | null | undefined): boolean {
		return !!value?.profiledeck_initialized
			&& value.provider_compatible
			&& value.config_status === "valid"
			&& value.auth_status === "valid";
	}
	function validateProfileID(value: string): string { const trimmed = value.trim(); if (!trimmed) return translate("profilePages.validation.idRequired"); if (trimmed.length > 80) return translate("profilePages.validation.idTooLong"); return /^[a-z0-9][a-z0-9._-]*$/.test(trimmed) ? "" : translate("profilePages.validation.idFormat"); }
	function validateOptionalName(value: string): string { return value.trim().length > 120 ? translate("profilePages.validation.nameTooLong") : ""; }
	function validateDescription(value: string): string { return value.trim().length > 1000 ? translate("profilePages.validation.descriptionTooLong") : ""; }
	function optional(value: string): string | null { return value.trim() || null; }
	function formatError(value: unknown): string { return desktopErrorMessage(value, translate("errors.desktopUnavailable")); }
</script>

{#if route.kind === "list"}
	<ContentContainer>
		<PageHeader title={$_("profilePages.list.title")} description={$_("profilePages.list.description")}>
			{#snippet actions()}
				<div class="flex items-center gap-2">
					<DropdownMenu.Root>
						<DropdownMenu.Trigger>
							{#snippet child({ props })}
								<Button {...props} size="icon-sm" variant="outline" aria-label={$_("actions.more")}>
									<MoreHorizontalIcon />
								</Button>
							{/snippet}
						</DropdownMenu.Trigger>
						<DropdownMenu.Content align="end">
							<DropdownMenu.Group>
								<DropdownMenu.Item onSelect={() => push("/codex/config-sets")}><SlidersHorizontalIcon />{$_("actions.configSets")}</DropdownMenu.Item>
								<DropdownMenu.Item onSelect={() => exportProfiles()}><DownloadIcon />{$_("actions.exportAllProfiles")}</DropdownMenu.Item>
								<DropdownMenu.Item onSelect={chooseProfileImport}><UploadIcon />{$_("actions.importProfiles")}</DropdownMenu.Item>
							</DropdownMenu.Group>
						</DropdownMenu.Content>
					</DropdownMenu.Root>
					<Tooltip.Root>
						<Tooltip.Trigger>
							{#snippet child({ props })}
								<Button
									{...props}
									size="sm"
									aria-disabled={!!busyAction || useBuilding || useApplying || !sourceReady}
									class="aria-disabled:opacity-50"
									onclick={(event) => {
										if (!!busyAction || useBuilding || useApplying || !sourceReady) {
											event.preventDefault();
											return;
										}
										push("/codex/profiles/new");
									}}
									aria-label={$_("actions.saveAsNewProfile")}
								>
									<PlusIcon data-icon="inline-start" />
									{$_("actions.saveCurrentShort")}
								</Button>
							{/snippet}
						</Tooltip.Trigger>
						<Tooltip.Content>{$_("actions.saveAsNewProfile")}</Tooltip.Content>
					</Tooltip.Root>
				</div>
			{/snippet}
		</PageHeader>

		{#if (detectResult !== null || !!detectError) && !sourceReady}
			<Alert.Root variant="destructive">
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("profilePages.source.notReadyTitle")}</Alert.Title>
				<Alert.Description>{sourceStatusDescription()}</Alert.Description>
				<Alert.Action>
					<div class="flex gap-2">
						<Button size="xs" variant="outline" onclick={() => { void Promise.all([refreshDetect(), refreshProfiles()]); }}>{$_("actions.retry")}</Button>
						<Button size="xs" variant="outline" onclick={() => push("/diagnostics")}>{$_("nav.diagnostics")}</Button>
					</div>
				</Alert.Action>
			</Alert.Root>
		{/if}

		<ProfileList
			profiles={listItems}
			loading={loadingProfiles}
			error={profileError}
			busy={!!busyAction || useBuilding || useApplying}
			canCreate={sourceReady}
			onNew={() => push("/codex/profiles/new")}
			onExport={(profile) => exportProfiles([profile.id])}
			onDelete={openProfileDelete}
			onRefreshQuota={(profile) => runtime.readQuota(profile.id)}
			onUse={openUse}
			onDetails={(profile) => push(`/codex/profiles/${encodeURIComponent(profile.id)}`)}
			onFork={(profile) => push(`/codex/profiles/${encodeURIComponent(profile.id)}/fork`)}
			onRetrySource={() => { void Promise.all([refreshDetect(), refreshProfiles()]); }}
		/>
	</ContentContainer>
{:else if route.kind === "config-sets"}
	<ConfigSetPage {configSets} loading={configSetsLoading} error={configSetsError} busy={!!busyAction} formatUpdated={formatRelativeTime} onBack={() => push("/codex/profiles")} onCreate={() => openConfigDialog("create")} onCopy={(value) => openConfigDialog("copy", value)} onEdit={(value) => openConfigDialog("edit", value)} onDelete={deleteConfigSet} />
{:else if route.kind === "new"}
	<ProfileEditorPage mode="new" {detectResult} canChooseConfigSet={!!activeProfileID} busy={!!busyAction} bind:profileID bind:profileName bind:profileDescription bind:configMode bind:credentialBinding bind:configBinding bind:newConfigSetID bind:newConfigSetName idError={displayedIDError} nameError={displayedNameError} descriptionError={displayedDescriptionError} onCancel={() => push("/codex/profiles")} onSubmit={createProfile} onRetrySource={() => { void refreshDetect(); }} onDiagnostics={() => { void push("/diagnostics"); }} />
{:else if detailLoading}
	<div class="mx-auto flex w-full max-w-5xl flex-col gap-4"><Skeleton class="h-5 w-48" /><Skeleton class="h-20 w-full" /><Skeleton class="h-52 w-full" /></div>
{:else if detailError || !detail}
	<div class="mx-auto w-full max-w-3xl"><Alert.Root variant="destructive"><AlertTriangleIcon data-icon="inline-start" /><Alert.Title>{$_("profilePages.errorTitle")}</Alert.Title><Alert.Description>{detailError || $_("errors.profileNotReady")}</Alert.Description></Alert.Root></div>
{:else if route.kind === "detail"}
	<ProfileDetail
		{detail}
		{busyAction}
		updated={formatRelativeTime(detail.summary.updated_at_unix_ms)}
		quota={runtime.quotaForSummary(detail.summary)}
		quotaLoading={runtime.isQuotaLoading(detail.summary.profile.id)}
		onRefreshQuota={() => runtime.readQuota(detail!.summary.profile.id)}
		onUse={() => openUse(profileListItem(detail!.summary))}
		onFork={() => push(`/codex/profiles/${encodeURIComponent(detail!.summary.profile.id)}/fork`)}
		onEdit={openEdit}
		onExport={() => exportProfiles([detail!.summary.profile.id])}
		onSaveCurrent={openSaveCurrent}
		onSetConfig={openSetConfig}
		onDelete={() => openProfileDelete({ id: detail!.summary.profile.id, name: detail!.summary.profile.name || translate("profile.unnamed") })}
	/>
{:else}
	<ProfileEditorPage mode="fork" {detail} {detectResult} busy={!!busyAction} bind:profileID bind:profileName bind:profileDescription bind:configMode bind:credentialBinding bind:configBinding bind:newConfigSetID bind:newConfigSetName idError={displayedIDError} nameError={displayedNameError} descriptionError={displayedDescriptionError} onCancel={() => push(`/codex/profiles/${encodeURIComponent(detail!.summary.profile.id)}`)} onSubmit={forkProfile} />
{/if}

<UseProfileDialog bind:open={useOpen} profile={useProfile} currentProfile={activeProfileID} plan={usePlan} building={useBuilding} applying={useApplying} inlineError={useInlineError} onClose={closeUse} onConfirm={confirmUse} />

<ProfileDeleteDialog bind:open={deleteOpen} profile={deleteTarget} onDeleted={profileDeleted} />

<ConfigSetDialog bind:open={configDialogOpen} mode={configDialog.mode} busy={busyAction === "config-set-save"} configSetID={configDialog.source?.id || ""} name={configDialog.source?.name || ""} description={configDialog.source?.description || ""} onClose={() => (configDialogOpen = false)} onSubmit={submitConfigDialog} />

<ProfileImportDialog bind:open={importOpen} plan={importPlan} busy={busyAction === "profile-import-apply"} onClose={closeProfileImport} onApply={applyProfileImport} />

<Dialog.Root bind:open={editOpen}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header><Dialog.Title>{$_("profilePages.edit.title")}</Dialog.Title><Dialog.Description>{$_("profilePages.edit.description")}</Dialog.Description></Dialog.Header>
		<Field.FieldGroup><Field.Field><Field.FieldLabel for="edit-profile-name">{$_("profilePages.form.name")}</Field.FieldLabel><Input id="edit-profile-name" bind:value={editName} /></Field.Field><Field.Field><Field.FieldLabel for="edit-profile-description">{$_("profilePages.form.description")}</Field.FieldLabel><Textarea id="edit-profile-description" bind:value={editDescription} rows={3} /></Field.Field></Field.FieldGroup>
		<Dialog.Footer><Button variant="outline" onclick={() => (editOpen = false)}>{$_("actions.cancel")}</Button><Button disabled={!editName.trim() || busyAction === "profile-metadata"} onclick={saveMetadata}>{#if busyAction === "profile-metadata"}<Spinner data-icon="inline-start" />{/if}{$_("actions.save")}</Button></Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={setConfigOpen}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header><Dialog.Title>{$_("actions.changeConfigSet")}</Dialog.Title><Dialog.Description>{$_("profilePages.detail.changeConfigDescription")}</Dialog.Description></Dialog.Header>
		<Select.Root type="single" value={selectedConfigSetID} onValueChange={(value) => (selectedConfigSetID = value)}>
			<Select.Trigger>{configSets.find((item) => item.id === selectedConfigSetID)?.name || $_("configSets.select")}</Select.Trigger>
			<Select.Content><Select.Group>{#each configSets as item (item.id)}<Select.Item value={item.id} label={`${item.name} · ${item.id}`} />{/each}</Select.Group></Select.Content>
		</Select.Root>
		<Dialog.Footer><Button variant="outline" onclick={() => (setConfigOpen = false)}>{$_("actions.cancel")}</Button><Button disabled={!selectedConfigSetID || busyAction === "profile-set-config"} onclick={setProfileConfig}>{$_("actions.save")}</Button></Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root bind:open={saveCurrentOpen}>
	<AlertDialog.Content>
		<AlertDialog.Header><AlertDialog.Title>{$_("profilePages.saveCurrent.title")}</AlertDialog.Title><AlertDialog.Description>{$_("profilePages.saveCurrent.description")}</AlertDialog.Description></AlertDialog.Header>
		{#if saveCurrentSourceError}
			<Alert.Root variant="destructive">
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("profilePages.source.notReadyTitle")}</Alert.Title>
				<Alert.Description>{saveCurrentSourceError}</Alert.Description>
			</Alert.Root>
		{/if}
		{#if (detail?.login?.reference_count ?? 0) > 1 || (detail?.config_set?.reference_count ?? 0) > 1}
			<Alert.Root>
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("profilePages.saveCurrent.sharedTitle")}</Alert.Title>
				<Alert.Description>
					{#if (detail?.login?.reference_count ?? 0) > 1 && (detail?.config_set?.reference_count ?? 0) > 1}
						{$_("profilePages.saveCurrent.sharedBothDescription", { values: { loginCount: detail?.login?.reference_count ?? 0, configCount: detail?.config_set?.reference_count ?? 0 } })}
					{:else if (detail?.login?.reference_count ?? 0) > 1}
						{$_("profilePages.saveCurrent.sharedLoginDescription", { values: { count: detail?.login?.reference_count ?? 0 } })}
					{:else}
						{$_("profilePages.saveCurrent.sharedConfigDescription", { values: { count: detail?.config_set?.reference_count ?? 0 } })}
					{/if}
				</Alert.Description>
			</Alert.Root>
		{/if}
		<AlertDialog.Footer><AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel><AlertDialog.Action onclick={saveCurrent}>{$_("actions.updateFromCurrent")}</AlertDialog.Action></AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
