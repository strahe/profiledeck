<script lang="ts">
	import { onMount, tick } from "svelte";
	import type { CancellablePromise } from "@wailsio/runtime";
	import { push } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import SlidersHorizontalIcon from "@lucide/svelte/icons/sliders-horizontal";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { Button } from "$lib/components/ui/button";
	import * as Dialog from "$lib/components/ui/dialog";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import * as Field from "$lib/components/ui/field";
	import { Input } from "$lib/components/ui/input";
	import * as Select from "$lib/components/ui/select";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { Textarea } from "$lib/components/ui/textarea";
	import * as Tooltip from "$lib/components/ui/tooltip";
	import { desktopErrorMessage, isCancelError, isDesktopErrorCode } from "$lib/desktop-errors";
	import { currentDesktopLocale, translate } from "$lib/i18n";
	import { joinUserMessages, profileChangeWarningMessage } from "$lib/user-facing-messages";

	import { GrokBuildService, SwitchService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CopyGrokBuildConfigSetRequest,
		CreateGrokBuildConfigSetRequest,
		CreateGrokBuildProfileRequest,
		ForkGrokBuildProfileRequest,
		UpdateGrokBuildConfigSetRequest,
		UpdateGrokBuildProfileMetadataRequest,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		ConfigSet,
		DetectResult,
		ProfileDetail as GrokBuildProfileDetail,
		ProfileSaveResult,
		ProfileSummary,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
	import type { SwitchPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/switching/models";

	import ConfigSetDialog from "./ConfigSetDialog.svelte";
	import ConfigSetPage from "./ConfigSetPage.svelte";
	import ProfileDeleteDialog, { type ProfileDeleteTarget } from "./ProfileDeleteDialog.svelte";
	import ProfileDetail from "./ProfileDetail.svelte";
	import ProfileEditorPage from "./ProfileEditorPage.svelte";
	import ProfileList from "./ProfileList.svelte";
	import UseProfileDialog from "./UseProfileDialog.svelte";
	import type {
		CodexForkBinding,
		ConfigSetDialogState,
		GrokBuildProfileRoute,
		ManagedProfileListItem,
		ProfileUseRequest,
	} from "./types";

	interface Props {
		route: GrokBuildProfileRoute;
		profiles: ProfileSummary[];
		dashboardConfigSets: ConfigSet[];
		detectResult: DetectResult | null;
		detectError: string;
		activeProfileID: string;
		loadingProfiles: boolean;
		profileError: string;
		useRequest: ProfileUseRequest | null;
		refreshDetect: () => Promise<DetectResult | null>;
		refreshProfiles: () => Promise<void>;
		cancelDetect: () => void;
		onUseRequestHandled: (sequence: number) => void;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	}

	let {
		route,
		profiles,
		dashboardConfigSets,
		detectResult,
		detectError,
		activeProfileID,
		loadingProfiles,
		profileError,
		useRequest,
		refreshDetect,
		refreshProfiles,
		cancelDetect,
		onUseRequestHandled,
		showError,
		showNotice,
	}: Props = $props();

	const providerID = "grok-build";
	const basePath = "/grok-build/profiles";
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let busyAction = $state("");
	let detail = $state<GrokBuildProfileDetail | null>(null);
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

	let configSets = $state<ConfigSet[]>([]);
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
	let deleteOpen = $state(false);
	let deleteTarget = $state<ProfileDeleteTarget | null>(null);

	let useOpen = $state(false);
	let useProfile = $state<ManagedProfileListItem | null>(null);
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
	let sharedConfigSetExists = $derived(configSets.some((value) => value.id === "shared"));
	let createRequiresCurrentConfig = $derived(
		configMode === "new" || (!activeProfileID && !sharedConfigSetExists),
	);
	let defaultCreateSourceReady = $derived(
		isSourceReady(detectResult, !activeProfileID && !sharedConfigSetExists),
	);
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

	async function enterRoute(next: GrokBuildProfileRoute) {
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
			const value = await track("profile-detail", GrokBuildService.ShowProfile(id));
			if (sequence !== routeSequence) return;
			detail = value;
			if (prepareFork) {
				profileID = `${value.summary.profile.id}-copy`;
				profileName = translate("profilePages.fork.copyName", {
					profile: value.summary.profile.name || value.summary.profile.id,
				});
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
		const request: CreateGrokBuildProfileRequest = {
			profile_id: profileID.trim(),
			name: optional(profileName),
			description: optional(profileDescription),
			new_config_set_id: configMode === "new" ? newConfigSetID.trim() : "",
			new_config_set_name: configMode === "new" ? optional(newConfigSetName) : null,
		};
		await runAction("profile-create", async () => {
			if (!isSourceReady(await refreshDetect(), createRequiresCurrentConfig)) return;
			const result = await track("profile-create", GrokBuildService.CreateProfile(request));
			await refreshProfiles();
			showResultWarnings(result);
			showNotice(
				translate("grokBuild.notice.createdTitle"),
				translate("grokBuild.notice.createdDescription", { profile: result.profile.name || result.profile.id }),
			);
			await push(`${basePath}/${encodeURIComponent(result.profile.id)}`);
		});
	}

	async function forkProfile() {
		formSubmitted = true;
		if (!detail || rawIDError || rawNameError || rawDescriptionError) return;
		const request: ForkGrokBuildProfileRequest = {
			source_profile_id: detail.summary.profile.id,
			profile_id: profileID.trim(),
			credential_binding: credentialBinding,
			config_binding: configBinding,
			new_config_set_id: configBinding === "copy-new" ? newConfigSetID.trim() : "",
			new_config_set_name: configBinding === "copy-new" ? optional(newConfigSetName) : null,
			name: optional(profileName),
			description: optional(profileDescription),
		};
		await runAction("profile-fork", async () => {
			const result = await track("profile-fork", GrokBuildService.ForkProfile(request));
			await refreshProfiles();
			showResultWarnings(result);
			showNotice(
				translate("grokBuild.notice.forkedTitle"),
				translate("grokBuild.notice.forkedDescription", { profile: result.profile.name || result.profile.id }),
			);
			await push(`${basePath}/${encodeURIComponent(result.profile.id)}`);
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
			void push(basePath);
		}
		void refreshProfiles();
		showNotice(translate("profileDelete.deletedTitle"), translate("profileDelete.deletedDescription"));
	}

	async function saveMetadata() {
		if (!detail || !editName.trim()) return;
		const request: UpdateGrokBuildProfileMetadataRequest = {
			profile_id: detail.summary.profile.id,
			name: editName.trim(),
			description: optional(editDescription),
		};
		await runAction("profile-metadata", async () => {
			await track("profile-metadata", GrokBuildService.UpdateProfileMetadata(request));
			editOpen = false;
			await Promise.all([refreshProfiles(), loadDetail(detail!.summary.profile.id)]);
			showNotice(translate("grokBuild.notice.updatedTitle"), translate("grokBuild.notice.updatedDescription"));
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
			const result = await track("profile-save-current", GrokBuildService.SaveActiveProfileState());
			saveCurrentOpen = false;
			await Promise.all([
				refreshProfiles(),
				refreshConfigSets(),
				detail ? loadDetail(detail.summary.profile.id) : Promise.resolve(),
			]);
			if (result.warnings?.length) {
				toast.warning(translate("notice.profileWarnings.title"), {
					description: joinUserMessages(result.warnings, profileChangeWarningMessage),
				});
			}
			showNotice(translate("grokBuild.notice.savedTitle"), translate("grokBuild.notice.savedDescription"));
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
			await track("profile-set-config", GrokBuildService.SetProfileConfig({
				profile_id: detail!.summary.profile.id,
				config_set_id: selectedConfigSetID,
			}));
			setConfigOpen = false;
			await Promise.all([refreshProfiles(), loadDetail(detail!.summary.profile.id), refreshConfigSets()]);
		});
	}

	async function refreshConfigSets() {
		configSetsLoading = true;
		try {
			const result = await track("config-sets", GrokBuildService.ListConfigSets());
			configSets = result.config_sets ?? [];
			configSetsError = "";
		} catch (error) {
			if (!isCancelError(error)) configSetsError = formatError(error);
		} finally {
			configSetsLoading = false;
		}
	}

	function openConfigDialog(mode: ConfigSetDialogState["mode"], source: ConfigSet | null = null) {
		configDialog = { mode, source };
		configDialogOpen = true;
	}

	async function submitConfigDialog(value: { id: string; name: string; description: string }) {
		await runAction("config-set-save", async () => {
			if (configDialog.mode === "create") {
				const request: CreateGrokBuildConfigSetRequest = {
					config_set_id: value.id,
					name: value.name,
					description: value.description,
				};
				await track("config-set-save", GrokBuildService.CreateConfigSet(request));
			} else if (configDialog.mode === "copy" && configDialog.source) {
				const request: CopyGrokBuildConfigSetRequest = {
					source_config_set_id: configDialog.source.id,
					config_set_id: value.id,
					name: value.name,
					description: value.description,
				};
				await track("config-set-save", GrokBuildService.CopyConfigSet(request));
			} else if (configDialog.source) {
				const request: UpdateGrokBuildConfigSetRequest = {
					config_set_id: configDialog.source.id,
					name: value.name,
					description: value.description,
				};
				await track("config-set-save", GrokBuildService.UpdateConfigSet(request));
			}
			configDialogOpen = false;
			await Promise.all([refreshConfigSets(), refreshProfiles()]);
		});
	}

	async function deleteConfigSet(configSet: ConfigSet) {
		await runAction("config-set-delete", async () => {
			await track("config-set-delete", GrokBuildService.DeleteConfigSet(configSet.id));
			await refreshConfigSets();
		});
	}

	async function openUse(profile: ManagedProfileListItem) {
		closeUse();
		const sequence = useSequence;
		useProfile = profile;
		useOpen = true;
		useBuilding = true;
		try {
			const plan = await track("use-build", SwitchService.BuildPlan(providerID, profile.id));
			if (sequence === useSequence) usePlan = plan;
		} catch (error) {
			if (sequence === useSequence && !isCancelError(error)) useInlineError = formatError(error);
		} finally {
			if (sequence === useSequence) useBuilding = false;
		}
	}

	async function openUseByProfileID(id: string) {
		let selected = listItems.find((item) => item.id === id);
		if (!selected) {
			await refreshProfiles();
			await tick();
			selected = listItems.find((item) => item.id === id);
		}
		if (selected) await openUse(selected);
		else showError({ code: "PROFILE_NOT_FOUND" });
	}

	async function confirmUse() {
		if (useApplying || !useProfile || !usePlan?.plan_fingerprint) return;
		const sequence = useSequence;
		useApplying = true;
		try {
			const result = await track("use-apply", SwitchService.Apply({
				provider_id: providerID,
				profile_id: useProfile.id,
				expected_plan_fingerprint: usePlan.plan_fingerprint,
				confirm: true,
			}));
			if (sequence !== useSequence) return;
			closeUse();
			await Promise.all([refreshProfiles(), refreshConfigSets()]);
			if (!result.recovery_cleanup_completed) {
				toast.warning(translate("notice.recoveryCleanup.switchAppliedTitle"), {
					description: translate("notice.recoveryCleanup.switchAppliedDescription", {
						profile: result.profile.name || result.profile.id,
					}),
				});
			} else {
				showNotice(
					translate("grokBuild.notice.switchedTitle"),
					translate("grokBuild.notice.switchedDescription", { profile: result.profile.name || result.profile.id }),
				);
			}
		} catch (error) {
			if (sequence !== useSequence || isCancelError(error)) return;
			if (isDesktopErrorCode(error, "TARGET_CHANGED")) {
				useInlineError = translate("errors.targetChanged");
				usePlan = null;
				try {
					usePlan = await track("use-build", SwitchService.BuildPlan(providerID, useProfile.id));
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
		try {
			await action();
		} catch (error) {
			if (!isCancelError(error)) showError(error);
		} finally {
			if (busyAction === name) busyAction = "";
		}
	}

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		cancelAction(key);
		inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => {
			if (inFlight.get(key) === promise) inFlight.delete(key);
		}).catch(() => {});
		return promise;
	}

	function cancelAction(key: string) {
		inFlight.get(key)?.cancel("replaced");
		inFlight.delete(key);
	}

	function cancelAll() {
		for (const promise of inFlight.values()) promise.cancel("route-change");
		inFlight.clear();
	}

	function showResultWarnings(result: ProfileSaveResult) {
		if (result.warnings?.length) {
			toast.warning(translate("notice.profileWarnings.title"), {
				description: joinUserMessages(result.warnings, profileChangeWarningMessage),
			});
		}
	}

	function profileListItem(summary: ProfileSummary): ManagedProfileListItem {
		return {
			summary,
			id: summary.profile.id,
			name: summary.profile.name || translate("profile.unnamed"),
			description: summary.profile.description || "",
		};
	}

	function formatRelativeTime(value: number | undefined): string {
		if (!value) return "—";
		const delta = Date.now() - value;
		if (delta < 60_000) return translate("time.justNow");
		if (delta < 3_600_000) {
			return translate("time.minutesAgo", { count: Math.max(1, Math.floor(delta / 60_000)) });
		}
		return new Date(value).toLocaleDateString(currentDesktopLocale(), { month: "short", day: "numeric" });
	}

	function sourceStatusDescription(value: DetectResult | null = detectResult): string {
		if (!value && detectError) return detectError;
		if (value?.file_auth_supported === false) return translate("grokBuild.profilePages.source.customAuth");
		return translate("grokBuild.profilePages.source.statusDescription", {
			config: translate(`sourceStatus.${value?.config_status || "missing"}`),
			auth: translate(`sourceStatus.${value?.auth_status || "missing"}`),
		});
	}

	function isSourceReady(value: DetectResult | null | undefined, requiresConfig = true): boolean {
		return !!value?.profiledeck_initialized
			&& value.provider_compatible
			&& value.file_auth_supported
			&& (!requiresConfig || value.config_status === "valid" || value.config_status === "missing")
			&& value.auth_status === "valid";
	}

	function validateProfileID(value: string): string {
		const trimmed = value.trim();
		if (!trimmed) return translate("profilePages.validation.idRequired");
		if (trimmed.length > 80) return translate("profilePages.validation.idTooLong");
		return /^[a-z0-9][a-z0-9._-]*$/.test(trimmed) ? "" : translate("profilePages.validation.idFormat");
	}

	function validateOptionalName(value: string): string {
		return value.trim().length > 120 ? translate("profilePages.validation.nameTooLong") : "";
	}

	function validateDescription(value: string): string {
		return value.trim().length > 1000 ? translate("profilePages.validation.descriptionTooLong") : "";
	}

	function optional(value: string): string | null {
		return value.trim() || null;
	}

	function formatError(value: unknown): string {
		return desktopErrorMessage(value, translate("errors.desktopUnavailable"));
	}
</script>

{#if route.kind === "list"}
	<ContentContainer>
		<PageHeader title={$_("grokBuild.profilePages.list.title")} description={$_("grokBuild.profilePages.list.description")}>
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
								<DropdownMenu.Item onSelect={() => push("/grok-build/config-sets")}>
									<SlidersHorizontalIcon />{$_("actions.configSets")}
								</DropdownMenu.Item>
							</DropdownMenu.Group>
						</DropdownMenu.Content>
					</DropdownMenu.Root>
					<Tooltip.Root>
						<Tooltip.Trigger>
							{#snippet child({ props })}
								<Button
									{...props}
									size="sm"
									aria-disabled={!!busyAction || useBuilding || useApplying || !defaultCreateSourceReady}
									class="aria-disabled:opacity-50"
									onclick={(event) => {
										if (!!busyAction || useBuilding || useApplying || !defaultCreateSourceReady) {
											event.preventDefault();
											return;
										}
										push("/grok-build/profiles/new");
									}}
									aria-label={$_("grokBuild.actions.saveAsNewProfile")}
								>
									<PlusIcon data-icon="inline-start" />
									{$_("grokBuild.actions.saveCurrentShort")}
								</Button>
							{/snippet}
						</Tooltip.Trigger>
						<Tooltip.Content>{$_("grokBuild.actions.saveAsNewProfile")}</Tooltip.Content>
					</Tooltip.Root>
				</div>
			{/snippet}
		</PageHeader>

		{#if (detectResult !== null || !!detectError) && !defaultCreateSourceReady}
			<Alert.Root variant="destructive">
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("grokBuild.profilePages.source.notReadyTitle")}</Alert.Title>
				<Alert.Description>{sourceStatusDescription()}</Alert.Description>
				<Alert.Action>
					<div class="flex gap-2">
						<Button
							size="xs"
							variant="outline"
							onclick={() => { void Promise.all([refreshDetect(), refreshProfiles()]); }}
						>
							{$_("actions.retry")}
						</Button>
						<Button size="xs" variant="outline" onclick={() => push("/diagnostics")}>
							{$_("nav.diagnostics")}
						</Button>
					</div>
				</Alert.Action>
			</Alert.Root>
		{/if}

		{#if detectResult?.warnings?.length}
			<Alert.Root>
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("grokBuild.profilePages.source.warningTitle")}</Alert.Title>
				<Alert.Description>{joinUserMessages(detectResult.warnings, profileChangeWarningMessage)}</Alert.Description>
			</Alert.Root>
		{/if}

		<ProfileList
			profiles={listItems}
			loading={loadingProfiles}
			error={profileError}
			busy={!!busyAction || useBuilding || useApplying}
			canCreate={defaultCreateSourceReady}
			emptyDescription={$_("grokBuild.profilePages.list.emptyDescription")}
			createLabel={$_("grokBuild.actions.saveAsNewProfile")}
			onNew={() => push("/grok-build/profiles/new")}
			onDelete={openProfileDelete}
			onUse={openUse}
			onDetails={(profile) => push(`${basePath}/${encodeURIComponent(profile.id)}`)}
			onFork={(profile) => push(`${basePath}/${encodeURIComponent(profile.id)}/fork`)}
			onRetrySource={() => { void Promise.all([refreshDetect(), refreshProfiles()]); }}
		/>
	</ContentContainer>
{:else if route.kind === "config-sets"}
	<ConfigSetPage
		{configSets}
		copyRoot="grokBuild.configSets"
		loading={configSetsLoading}
		error={configSetsError}
		busy={!!busyAction}
		formatUpdated={formatRelativeTime}
		onBack={() => push(basePath)}
		onCreate={() => openConfigDialog("create")}
		onCopy={(value) => openConfigDialog("copy", value as ConfigSet)}
		onEdit={(value) => openConfigDialog("edit", value as ConfigSet)}
		onDelete={(value) => deleteConfigSet(value as ConfigSet)}
	/>
{:else if route.kind === "new"}
	<ProfileEditorPage
		mode="new"
		{detectResult}
		{basePath}
		copyRoot="grokBuild.profilePages"
		allowMissingConfig
		requiresCurrentConfig={createRequiresCurrentConfig}
		sourceError={sourceStatusDescription()}
		createActionLabel={$_("grokBuild.actions.saveAsNewProfile")}
		canChooseConfigSet={!!activeProfileID}
		busy={!!busyAction}
		bind:profileID
		bind:profileName
		bind:profileDescription
		bind:configMode
		bind:credentialBinding
		bind:configBinding
		bind:newConfigSetID
		bind:newConfigSetName
		idError={displayedIDError}
		nameError={displayedNameError}
		descriptionError={displayedDescriptionError}
		onCancel={() => push(basePath)}
		onSubmit={createProfile}
		onRetrySource={() => { void refreshDetect(); }}
		onDiagnostics={() => { void push("/diagnostics"); }}
	/>
{:else if detailLoading}
	<div class="mx-auto flex w-full max-w-5xl flex-col gap-4">
		<Skeleton class="h-5 w-48" />
		<Skeleton class="h-20 w-full" />
		<Skeleton class="h-52 w-full" />
	</div>
{:else if detailError || !detail}
	<div class="mx-auto w-full max-w-3xl">
		<Alert.Root variant="destructive">
			<AlertTriangleIcon data-icon="inline-start" />
			<Alert.Title>{$_("profilePages.errorTitle")}</Alert.Title>
			<Alert.Description>{detailError || $_("errors.profileNotReady")}</Alert.Description>
		</Alert.Root>
	</div>
{:else if route.kind === "detail"}
	<ProfileDetail
		{detail}
		mode="grok-build"
		{basePath}
		updateFromCurrentLabel={$_("grokBuild.actions.updateFromCurrent")}
		{busyAction}
		updated={formatRelativeTime(detail.summary.updated_at_unix_ms)}
		onUse={() => openUse(profileListItem(detail!.summary))}
		onFork={() => push(`${basePath}/${encodeURIComponent(detail!.summary.profile.id)}/fork`)}
		onEdit={openEdit}
		onSaveCurrent={openSaveCurrent}
		onSetConfig={openSetConfig}
		onDelete={() => openProfileDelete({
			id: detail!.summary.profile.id,
			name: detail!.summary.profile.name || translate("profile.unnamed"),
		})}
	/>
{:else}
	<ProfileEditorPage
		mode="fork"
		{detail}
		{detectResult}
		{basePath}
		copyRoot="grokBuild.profilePages"
		allowMissingConfig
		createActionLabel={$_("grokBuild.actions.saveAsNewProfile")}
		busy={!!busyAction}
		bind:profileID
		bind:profileName
		bind:profileDescription
		bind:configMode
		bind:credentialBinding
		bind:configBinding
		bind:newConfigSetID
		bind:newConfigSetName
		idError={displayedIDError}
		nameError={displayedNameError}
		descriptionError={displayedDescriptionError}
		onCancel={() => push(`${basePath}/${encodeURIComponent(detail!.summary.profile.id)}`)}
		onSubmit={forkProfile}
	/>
{/if}

<UseProfileDialog
	bind:open={useOpen}
	profile={useProfile}
	agent="Grok Build"
	mode="grok-build"
	currentProfile={activeProfileID}
	plan={usePlan}
	building={useBuilding}
	applying={useApplying}
	inlineError={useInlineError}
	onClose={closeUse}
	onConfirm={confirmUse}
/>

<ProfileDeleteDialog bind:open={deleteOpen} profile={deleteTarget} onDeleted={profileDeleted} />

<ConfigSetDialog
	bind:open={configDialogOpen}
	mode={configDialog.mode}
	copyRoot="grokBuild.configSets"
	busy={busyAction === "config-set-save"}
	configSetID={configDialog.source?.id || ""}
	name={configDialog.source?.name || ""}
	description={configDialog.source?.description || ""}
	onClose={() => (configDialogOpen = false)}
	onSubmit={submitConfigDialog}
/>

<Dialog.Root bind:open={editOpen}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>{$_("profilePages.edit.title")}</Dialog.Title>
			<Dialog.Description>{$_("grokBuild.profilePages.edit.description")}</Dialog.Description>
		</Dialog.Header>
		<Field.FieldGroup>
			<Field.Field>
				<Field.FieldLabel for="edit-grok-profile-name">{$_("profilePages.form.name")}</Field.FieldLabel>
				<Input id="edit-grok-profile-name" bind:value={editName} />
			</Field.Field>
			<Field.Field>
				<Field.FieldLabel for="edit-grok-profile-description">{$_("profilePages.form.description")}</Field.FieldLabel>
				<Textarea id="edit-grok-profile-description" bind:value={editDescription} rows={3} />
			</Field.Field>
		</Field.FieldGroup>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (editOpen = false)}>{$_("actions.cancel")}</Button>
			<Button disabled={!editName.trim() || busyAction === "profile-metadata"} onclick={saveMetadata}>
				{#if busyAction === "profile-metadata"}<Spinner data-icon="inline-start" />{/if}
				{$_("actions.save")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={setConfigOpen}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title>{$_("actions.changeConfigSet")}</Dialog.Title>
			<Dialog.Description>{$_("grokBuild.profilePages.detail.changeConfigDescription")}</Dialog.Description>
		</Dialog.Header>
		<Select.Root type="single" value={selectedConfigSetID} onValueChange={(value) => (selectedConfigSetID = value)}>
			<Select.Trigger>{configSets.find((item) => item.id === selectedConfigSetID)?.name || $_("grokBuild.configSets.select")}</Select.Trigger>
			<Select.Content>
				<Select.Group>
					{#each configSets as item (item.id)}
						<Select.Item value={item.id} label={`${item.name} · ${item.id}`} />
					{/each}
				</Select.Group>
			</Select.Content>
		</Select.Root>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (setConfigOpen = false)}>{$_("actions.cancel")}</Button>
			<Button disabled={!selectedConfigSetID || busyAction === "profile-set-config"} onclick={setProfileConfig}>
				{$_("actions.save")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root bind:open={saveCurrentOpen}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			<AlertDialog.Title>{$_("grokBuild.profilePages.saveCurrent.title")}</AlertDialog.Title>
			<AlertDialog.Description>{$_("grokBuild.profilePages.saveCurrent.description")}</AlertDialog.Description>
		</AlertDialog.Header>
		{#if saveCurrentSourceError}
			<Alert.Root variant="destructive">
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("grokBuild.profilePages.source.notReadyTitle")}</Alert.Title>
				<Alert.Description>{saveCurrentSourceError}</Alert.Description>
			</Alert.Root>
		{/if}
		{#if (detail?.login?.reference_count ?? 0) > 1 || (detail?.config_set?.reference_count ?? 0) > 1}
			<Alert.Root>
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("profilePages.saveCurrent.sharedTitle")}</Alert.Title>
				<Alert.Description>
					{#if (detail?.login?.reference_count ?? 0) > 1 && (detail?.config_set?.reference_count ?? 0) > 1}
						{$_("profilePages.saveCurrent.sharedBothDescription", {
							values: {
								loginCount: detail?.login?.reference_count ?? 0,
								configCount: detail?.config_set?.reference_count ?? 0,
							},
						})}
					{:else if (detail?.login?.reference_count ?? 0) > 1}
						{$_("profilePages.saveCurrent.sharedLoginDescription", {
							values: { count: detail?.login?.reference_count ?? 0 },
						})}
					{:else}
						{$_("profilePages.saveCurrent.sharedConfigDescription", {
							values: { count: detail?.config_set?.reference_count ?? 0 },
						})}
					{/if}
				</Alert.Description>
			</Alert.Root>
		{/if}
		<AlertDialog.Footer>
			<AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel>
			<AlertDialog.Action onclick={saveCurrent}>{$_("grokBuild.actions.updateFromCurrent")}</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
