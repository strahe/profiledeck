<script lang="ts">
	import { onMount, tick } from "svelte";
	import { type CancellablePromise } from "@wailsio/runtime";
	import { push } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
	import CheckIcon from "@lucide/svelte/icons/check";
	import EyeIcon from "@lucide/svelte/icons/eye";
	import KeyRoundIcon from "@lucide/svelte/icons/key-round";
	import MoreHorizontalIcon from "@lucide/svelte/icons/more-horizontal";
	import PencilIcon from "@lucide/svelte/icons/pencil";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import Trash2Icon from "@lucide/svelte/icons/trash-2";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import {
		AntigravityService,
		SwitchService,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CreateAntigravityProfileRequest,
		UpdateAntigravityProfileRequest,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		AntigravityDetectResult,
		AntigravityProfileDetail,
		AntigravityProfileQuota,
		AntigravityProfileSummary,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/antigravity/models";
	import type { SwitchPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/switching/models";

	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import IconAction from "$lib/components/app/IconAction.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import InfoTooltip from "$lib/components/app/InfoTooltip.svelte";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import * as Card from "$lib/components/ui/card";
	import * as Dialog from "$lib/components/ui/dialog";
	import * as DropdownMenu from "$lib/components/ui/dropdown-menu";
	import * as Empty from "$lib/components/ui/empty";
	import * as Field from "$lib/components/ui/field";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Separator } from "$lib/components/ui/separator";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { Textarea } from "$lib/components/ui/textarea";
	import { desktopErrorMessage, isCancelError, isDesktopErrorCode } from "$lib/desktop-errors";
	import { translate } from "$lib/i18n";
	import { cn } from "$lib/utils";
	import { joinUserMessages, profileChangeWarningMessage, profileWarningMessage } from "$lib/user-facing-messages";

	import AntigravityQuotaCard from "./AntigravityQuotaCard.svelte";
	import AntigravityQuotaSummary from "./AntigravityQuotaSummary.svelte";
	import ProfileQuotaFreshness from "./ProfileQuotaFreshness.svelte";
	import ProfileDeleteDialog, { type ProfileDeleteTarget } from "./ProfileDeleteDialog.svelte";
	import UseProfileDialog from "./UseProfileDialog.svelte";
	import type { AntigravityQuotaCheck } from "./antigravity-quota.svelte.js";
	import type { AntigravityProfileRoute, ProfileUseRequest, SwitchProfileItem } from "./types";

	interface Props {
		route: AntigravityProfileRoute;
		profiles: AntigravityProfileSummary[];
		detectResult: AntigravityDetectResult | null;
		detectError: string;
		activeProfileID: string;
		loadingProfiles: boolean;
		profileError: string;
		useRequest: ProfileUseRequest | null;
		refreshDetect: () => Promise<AntigravityDetectResult | null>;
		refreshProfiles: () => Promise<void>;
		quotaForSummary: (summary: AntigravityProfileSummary) => AntigravityProfileQuota | null;
		quotaCheckForSummary: (summary: AntigravityProfileSummary) => AntigravityQuotaCheck;
		quotaLoading: (profileID: string) => boolean;
		refreshQuota: (profileID: string) => Promise<AntigravityProfileQuota | null>;
		onUseRequestHandled: (sequence: number) => void;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	}

	let {
		route, profiles, detectResult, detectError, activeProfileID, loadingProfiles, profileError,
		useRequest, refreshDetect, refreshProfiles, quotaForSummary, quotaCheckForSummary, quotaLoading,
		refreshQuota, onUseRequestHandled, showError, showNotice,
	}: Props = $props();

	const providerID = "antigravity";
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let busyAction = $state("");
	let detail = $state<AntigravityProfileDetail | null>(null);
	let detailLoading = $state(false);
	let detailError = $state("");
	let routeKey = "";
	let routeSequence = 0;
	let nowUnixMS = $state(Date.now());

	let profileID = $state("");
	let profileName = $state("");
	let profileDescription = $state("");
	let formSubmitted = $state(false);

	let editOpen = $state(false);
	let editName = $state("");
	let editDescription = $state("");
	let saveCurrentOpen = $state(false);
	let saveCurrentReferenceCount = $state(1);
	let deleteOpen = $state(false);
	let deleteTarget = $state<ProfileDeleteTarget | null>(null);

	let useOpen = $state(false);
	let useProfile = $state<SwitchProfileItem | null>(null);
	let usePlan = $state<SwitchPlan | null>(null);
	let useBuilding = $state(false);
	let useApplying = $state(false);
	let useInlineError = $state("");
	let useSequence = 0;
	let handledUseSequence = 0;

	let listItems = $derived.by(() => {
		void $locale;
		return [...profiles]
			.sort((left, right) => Number(right.active) - Number(left.active) || left.profile.id.localeCompare(right.profile.id))
			.map((summary) => ({ id: summary.profile.id, name: summary.profile.name || translate("profile.unnamed") }));
	});
	let providerReady = $derived(!!detectResult?.profiledeck_initialized && detectResult.provider_compatible);
	let sourceReady = $derived(providerReady
		&& detectResult?.credential_status === "valid");
	let switchReady = $derived(providerReady
		&& (detectResult?.credential_status === "valid" || detectResult?.credential_status === "missing"));
	let activeLoginMissing = $derived(detectResult?.credential_status === "missing");
	let rawIDError = $derived(validateProfileID(profileID));
	let rawNameError = $derived(validateOptionalName(profileName));
	let rawDescriptionError = $derived(validateDescription(profileDescription));
	let displayedIDError = $derived(formSubmitted || profileID ? rawIDError : "");
	let displayedNameError = $derived(formSubmitted || profileName ? rawNameError : "");
	let displayedDescriptionError = $derived(formSubmitted || profileDescription ? rawDescriptionError : "");

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
		const timer = window.setInterval(() => {
			nowUnixMS = Date.now();
		}, 60_000);
		return () => {
			window.clearInterval(timer);
			cancelAll();
		};
	});

	async function enterRoute(next: AntigravityProfileRoute) {
		const sequence = ++routeSequence;
		cancelAll();
		closeUse();
		busyAction = "";
		detailError = "";
		formSubmitted = false;
		editOpen = false;
		if (next.kind === "list") {
			detail = null;
			resetForm();
			await Promise.all([refreshDetect(), refreshProfiles()]);
			return;
		}
		if (next.kind === "new") {
			detail = null;
			resetForm();
			await Promise.all([refreshDetect(), refreshProfiles()]);
			return;
		}
		await Promise.all([loadDetail(next.profileID, sequence), refreshDetect(), refreshProfiles()]);
	}

	async function loadDetail(id: string, sequence = routeSequence) {
		detailLoading = true;
		try {
			const result = await track("antigravity-detail", AntigravityService.ShowProfile(id));
			if (sequence === routeSequence) detail = result;
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
	}

	async function createProfile() {
		formSubmitted = true;
		if (rawIDError || rawNameError || rawDescriptionError) return;
		await runAction("antigravity-create", async () => {
			if (!isSourceReady(await refreshDetect())) return;
			const request: CreateAntigravityProfileRequest = {
				profile_id: profileID.trim(),
				name: optional(profileName),
				description: optional(profileDescription),
			};
			const result = await track("antigravity-create", AntigravityService.CreateProfile(request));
			await refreshProfiles();
			showWarnings(result.warnings);
			showNotice(translate("antigravity.notice.createdTitle"), translate("antigravity.notice.createdDescription", { profile: result.summary.profile.name || result.summary.profile.id }));
			await push(`/antigravity/profiles/${encodeURIComponent(result.summary.profile.id)}`);
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
			void push("/antigravity/profiles");
		}
		void refreshProfiles();
		showNotice(translate("profileDelete.deletedTitle"), translate("profileDelete.deletedDescription"));
	}

	function openSaveCurrent(referenceCount: number) {
		saveCurrentReferenceCount = Math.max(1, referenceCount);
		saveCurrentOpen = true;
	}



	async function saveMetadata() {
		if (!detail || !editName.trim() || validateOptionalName(editName) || validateDescription(editDescription)) return;
		await runAction("antigravity-edit", async () => {
			const request: UpdateAntigravityProfileRequest = {
				profile_id: detail!.summary.profile.id,
				name: editName.trim(),
				description: editDescription.trim(),
			};
			const result = await track("antigravity-edit", AntigravityService.UpdateProfile(request));
			detail = result;
			editOpen = false;
			await refreshProfiles();
			showNotice(translate("antigravity.notice.updatedTitle"), translate("antigravity.notice.updatedDescription"));
		});
	}

	async function saveCurrent() {
		await runAction("antigravity-save-current", async () => {
			if (!isSourceReady(await refreshDetect())) return;
			const result = await track("antigravity-save-current", AntigravityService.SaveCurrent());
			saveCurrentOpen = false;
			await Promise.all([refreshProfiles(), detail ? loadDetail(detail.summary.profile.id) : Promise.resolve()]);
			showWarnings(result.warnings);
			showNotice(translate("antigravity.notice.savedTitle"), translate("antigravity.notice.savedDescription"));
		});
	}

	async function openUse(profile: SwitchProfileItem) {
		closeUse();
		const sequence = useSequence;
		useProfile = profile;
		useOpen = true;
		useBuilding = true;
		try {
			const plan = await track("antigravity-use-build", SwitchService.BuildPlan(providerID, profile.id));
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
			const activatedProfileID = useProfile.id;
			const result = await track("antigravity-use-apply", SwitchService.Apply({
				provider_id: providerID,
				profile_id: useProfile.id,
				expected_plan_fingerprint: usePlan.plan_fingerprint,
				confirm: true,
			}));
			if (sequence !== useSequence) return;
			closeUse();
			await Promise.all([
				refreshProfiles(),
				refreshDetect(),
				detail ? loadDetail(activatedProfileID) : Promise.resolve(),
			]);
			if (!result.recovery_cleanup_completed) {
				toast.warning(translate("notice.recoveryCleanup.switchAppliedTitle"), {
					description: translate("notice.recoveryCleanup.switchAppliedDescription", { profile: result.profile.name || result.profile.id }),
				});
			} else {
				showNotice(translate("antigravity.notice.switchedTitle"), translate("antigravity.notice.switchedDescription", { profile: result.profile.name || result.profile.id }));
			}
		} catch (error) {
			if (sequence !== useSequence || isCancelError(error)) return;
			if (isDesktopErrorCode(error, "TARGET_CHANGED")) {
				useInlineError = translate("antigravity.use.changed");
				usePlan = null;
				try {
					usePlan = await track("antigravity-use-build", SwitchService.BuildPlan(providerID, useProfile.id));
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
		cancelAction("antigravity-use-build");
		cancelAction("antigravity-use-apply");
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
		promise.finally(() => { if (inFlight.get(key) === promise) inFlight.delete(key); }).catch(() => {});
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

	function isSourceReady(value: AntigravityDetectResult | null | undefined): boolean {
		return !!value?.profiledeck_initialized && value.provider_compatible && value.credential_status === "valid";
	}

	function sourceDescription(): string {
		if (!detectResult && detectError) return detectError;
		if (detectResult && !detectResult.profiledeck_initialized) return translate("antigravity.source.notInitialized");
		if (detectResult && !detectResult.provider_compatible) return translate("antigravity.source.incompatible");
		return translate(`antigravity.source.${detectResult?.credential_status || "missing"}`);
	}

	function showID(summary: AntigravityProfileSummary): boolean {
		const name = summary.profile.name.trim();
		if (!name) return true;
		return profiles.filter((item) => item.profile.name.trim() === name).length > 1;
	}

	function shortID(value: string): string {
		return value.length > 8 ? `…${value.slice(-8)}` : value;
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

	function showWarnings(warnings: string[] | null | undefined) {
		if (warnings?.length) toast.warning(translate("antigravity.notice.warningTitle"), { description: joinUserMessages(warnings, profileChangeWarningMessage) });
	}
</script>

{#if route.kind === "list"}
	<ContentContainer>
		<PageHeader title={$_("antigravity.list.title")} description={$_("antigravity.list.description")}>
			{#snippet actions()}
				<Button disabled={!sourceReady || !!busyAction} onclick={() => push("/antigravity/profiles/new")}>
					<PlusIcon data-icon="inline-start" />
					{$_("antigravity.actions.newProfile")}
				</Button>
			{/snippet}
		</PageHeader>

		{#if (detectResult !== null || !!detectError) && !sourceReady}
			<Alert.Root variant={detectResult?.credential_status === "invalid" ? "destructive" : "default"}>
				<TriangleAlertIcon data-icon="inline-start" />
				<Alert.Title>{$_("antigravity.source.notReadyTitle")}</Alert.Title>
				<Alert.Description>{sourceDescription()}</Alert.Description>
				<Alert.Action><Button size="xs" variant="outline" onclick={() => { void refreshDetect(); }}>{$_("actions.retry")}</Button></Alert.Action>
			</Alert.Root>
		{/if}

		<Card.Root size="sm">
			<Card.Content class="px-0">

				{#if loadingProfiles}
					<div class="flex flex-col gap-3 px-4 pb-4">
						{#each [1, 2, 3] as item (item)}
							<div class="flex items-center justify-between gap-4 py-2">
								<div class="flex flex-col gap-2"><Skeleton class="h-4 w-40" /><Skeleton class="h-3 w-56" /></div>
								<Skeleton class="h-8 w-24" />
							</div>
						{/each}
					</div>
				{:else if profileError}
					<div class="px-4 pb-4">
						<Alert.Root variant="destructive">
							<TriangleAlertIcon data-icon="inline-start" />
							<Alert.Title>{$_("empty.loadProfilesFailedTitle")}</Alert.Title>
							<Alert.Description>{profileError}</Alert.Description>
						</Alert.Root>
					</div>
				{:else if profiles.length === 0}
					<Empty.Root class="border-0 py-12">
						<Empty.Header>
							<Empty.Media variant="icon"><KeyRoundIcon /></Empty.Media>
							<Empty.Title>{$_("antigravity.list.emptyTitle")}</Empty.Title>
							<Empty.Description>{$_("antigravity.list.emptyDescription")}</Empty.Description>
						</Empty.Header>
						{#if sourceReady}
							<Empty.Content>
								<Button onclick={() => push("/antigravity/profiles/new")}><PlusIcon data-icon="inline-start" />{$_("antigravity.actions.newProfile")}</Button>
							</Empty.Content>
						{/if}
					</Empty.Root>
				{:else}
					{#each profiles as summary, index (summary.profile.id)}
						{@const quota = quotaForSummary(summary)}
						{@const quotaCheck = quotaCheckForSummary(summary)}
						{@const isQuotaLoading = quotaLoading(summary.profile.id)}
						<div class={cn("flex flex-col gap-2.5 px-4 py-3", summary.active && "bg-primary/5 ring-1 ring-inset ring-primary/20")}>
							<div class="flex min-w-0 items-center gap-4">
								<div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
									<Button variant="link" class="h-auto min-w-0 justify-start p-0" onclick={() => push(`/antigravity/profiles/${encodeURIComponent(summary.profile.id)}`)}>
										<span class="truncate">{summary.profile.name || $_("profile.unnamed")}</span>
									</Button>
									{#if summary.profile.description}<InfoTooltip content={summary.profile.description} subject={summary.profile.name || summary.profile.id} />{/if}
									{#if showID(summary)}<span class="font-mono text-xs text-muted-foreground">{shortID(summary.profile.id)}</span>{/if}
									{#if summary.active}<StatusBadge tone="current"><CheckIcon />{$_("status.current")}</StatusBadge>{/if}
									{#if summary.warnings?.length}<StatusBadge tone="warning">{$_("status.warning")}</StatusBadge>{/if}
								</div>

								<div class="ml-auto flex shrink-0 items-center gap-2">
									<ProfileQuotaFreshness checkedAtUnixMS={quotaCheck.checkedAtUnixMS} checkOutcome={quotaCheck.outcome} {nowUnixMS} />
									{#if summary.active}
										<IconAction
											label={$_("actions.refreshProfileQuota", { values: { profile: summary.profile.name || $_("profile.unnamed") } })}
											disabled={isQuotaLoading}
											onclick={() => { void refreshQuota(summary.profile.id); }}
										>
											{#if isQuotaLoading}<Spinner />{:else}<RefreshCwIcon />{/if}
										</IconAction>
									{/if}
									{#if !summary.active || activeLoginMissing}
										<Button size="sm" disabled={!switchReady || !!busyAction || useBuilding || useApplying} onclick={() => openUse({ id: summary.profile.id, name: summary.profile.name || $_("profile.unnamed") })}>{$_("actions.useProfile")}</Button>
									{/if}
									<DropdownMenu.Root>
										<DropdownMenu.Trigger>
											{#snippet child({ props })}<Button {...props} variant="outline" size="icon-sm" aria-label={$_("actions.more")}><MoreHorizontalIcon /></Button>{/snippet}
										</DropdownMenu.Trigger>
										<DropdownMenu.Content align="end">
											<DropdownMenu.Group>
												<DropdownMenu.Item onSelect={() => push(`/antigravity/profiles/${encodeURIComponent(summary.profile.id)}`)}><EyeIcon />{$_("actions.details")}</DropdownMenu.Item>
												{#if summary.active}<DropdownMenu.Item disabled={!sourceReady || !!busyAction} onSelect={() => openSaveCurrent(summary.credential_reference_count)}><RefreshCwIcon />{$_("antigravity.actions.updateCurrent")}</DropdownMenu.Item>{/if}
												<DropdownMenu.Item variant="destructive" disabled={!!busyAction} onSelect={() => openProfileDelete({ id: summary.profile.id, name: summary.profile.name || $_("profile.unnamed") })}><Trash2Icon />{$_("actions.deleteProfile")}</DropdownMenu.Item>
											</DropdownMenu.Group>
										</DropdownMenu.Content>
									</DropdownMenu.Root>
								</div>
							</div>
							<AntigravityQuotaSummary {quota} loading={isQuotaLoading} {nowUnixMS} />

							{#if summary.warnings?.length}
								<Alert.Root>
									<TriangleAlertIcon data-icon="inline-start" />
									<Alert.Title>{$_("antigravity.notice.warningTitle")}</Alert.Title>
									<Alert.Description>{joinUserMessages(summary.warnings, profileWarningMessage)}</Alert.Description>
								</Alert.Root>
							{/if}
						</div>
						{#if index < profiles.length - 1}<Separator />{/if}
					{/each}
				{/if}
			</Card.Content>
		</Card.Root>
	</ContentContainer>
{:else if route.kind === "new"}
	<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
		<Button class="self-start" variant="ghost" onclick={() => push("/antigravity/profiles")}><ArrowLeftIcon data-icon="inline-start" />{$_("actions.backToProfiles")}</Button>
		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("antigravity.new.title")}</Card.Title>
				<Card.Description>{$_("antigravity.new.description")}</Card.Description>
			</Card.Header>
			<Card.Content class="flex flex-col gap-4">
				<Alert.Root variant={sourceReady ? "default" : "destructive"}>
					<KeyRoundIcon data-icon="inline-start" />
					<Alert.Title>{sourceReady ? $_("antigravity.source.readyTitle") : $_("antigravity.source.notReadyTitle")}</Alert.Title>
					<Alert.Description>{sourceDescription()}</Alert.Description>
				</Alert.Root>
				<Field.FieldGroup>
					<Field.Field data-invalid={!!displayedIDError}>
						<Field.FieldLabel for="antigravity-profile-id">{$_("profilePages.form.profileID")}</Field.FieldLabel>
						<Input id="antigravity-profile-id" bind:value={profileID} aria-invalid={!!displayedIDError} placeholder={$_("profilePages.form.profileIDPlaceholder")} />
						{#if displayedIDError}<Field.FieldError>{displayedIDError}</Field.FieldError>{:else}<Field.FieldDescription>{$_("profilePages.form.profileIDHelp")}</Field.FieldDescription>{/if}
					</Field.Field>
					<Field.Field data-invalid={!!displayedNameError}>
						<Field.FieldLabel for="antigravity-profile-name">{$_("profilePages.form.name")}</Field.FieldLabel>
						<Input id="antigravity-profile-name" bind:value={profileName} aria-invalid={!!displayedNameError} placeholder={$_("profilePages.form.namePlaceholder")} />
						{#if displayedNameError}<Field.FieldError>{displayedNameError}</Field.FieldError>{:else}<Field.FieldDescription>{$_("profilePages.form.nameHelp")}</Field.FieldDescription>{/if}
					</Field.Field>
					<Field.Field data-invalid={!!displayedDescriptionError}>
						<Field.FieldLabel for="antigravity-profile-description">{$_("profilePages.form.description")}</Field.FieldLabel>
						<Textarea id="antigravity-profile-description" bind:value={profileDescription} aria-invalid={!!displayedDescriptionError} rows={3} placeholder={$_("profilePages.form.descriptionPlaceholder")} />
						{#if displayedDescriptionError}<Field.FieldError>{displayedDescriptionError}</Field.FieldError>{:else}<Field.FieldDescription>{$_("profilePages.form.descriptionHelp")}</Field.FieldDescription>{/if}
					</Field.Field>
				</Field.FieldGroup>
			</Card.Content>
			<Card.Footer class="justify-end gap-2">
				<Button variant="outline" onclick={() => push("/antigravity/profiles")}>{$_("actions.cancel")}</Button>
				<Button disabled={!sourceReady || !!busyAction} onclick={createProfile}>{#if busyAction === "antigravity-create"}<Spinner data-icon="inline-start" />{/if}{$_("antigravity.actions.saveProfile")}</Button>
			</Card.Footer>
		</Card.Root>
	</div>
{:else if detailLoading}
	<div class="mx-auto flex w-full max-w-3xl flex-col gap-4"><Skeleton class="h-5 w-48" /><Skeleton class="h-72 w-full" /></div>
{:else if detailError || !detail}
	<div class="mx-auto w-full max-w-3xl"><Alert.Root variant="destructive"><TriangleAlertIcon data-icon="inline-start" /><Alert.Title>{$_("profilePages.errorTitle")}</Alert.Title><Alert.Description>{detailError || $_("errors.profileNotReady")}</Alert.Description></Alert.Root></div>
{:else}
	{@const detailQuota = quotaForSummary(detail.summary)}
	{@const detailQuotaCheck = quotaCheckForSummary(detail.summary)}
	{@const detailQuotaLoading = quotaLoading(detail.summary.profile.id)}
	<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
		<Button class="self-start" variant="ghost" onclick={() => push("/antigravity/profiles")}><ArrowLeftIcon data-icon="inline-start" />{$_("actions.backToProfiles")}</Button>
		{#if (detectResult !== null || !!detectError) && !sourceReady}
			<Alert.Root variant={detectResult?.credential_status === "invalid" ? "destructive" : "default"}>
				<TriangleAlertIcon data-icon="inline-start" />
				<Alert.Title>{$_("antigravity.source.notReadyTitle")}</Alert.Title>
				<Alert.Description>{sourceDescription()}</Alert.Description>
				<Alert.Action><Button size="xs" variant="outline" onclick={() => { void refreshDetect(); }}>{$_("actions.retry")}</Button></Alert.Action>
			</Alert.Root>
		{/if}
		<Card.Root>
			<Card.Header>
				<Card.Title>{detail.summary.profile.name || $_("profile.unnamed")}</Card.Title>
				<Card.Description>{detail.summary.profile.description || $_("profile.noDescription")}</Card.Description>
				{#if detail.summary.active}<Card.Action><Badge>{$_("status.current")}</Badge></Card.Action>{/if}
			</Card.Header>
			<Card.Content class="flex flex-col gap-4">
				<dl class="grid gap-4 text-sm sm:grid-cols-2">
					<div class="flex flex-col gap-1"><dt class="text-muted-foreground">{$_("profilePages.form.profileID")}</dt><dd class="font-mono">{detail.summary.profile.id}</dd></div>
					<div class="flex flex-col gap-1"><dt class="text-muted-foreground">{$_("antigravity.detail.loginSource")}</dt><dd>{detail.summary.active ? $_("antigravity.detail.currentLogin") : $_("antigravity.detail.savedLogin")}</dd></div>
					<div class="flex flex-col gap-1"><dt class="text-muted-foreground">{$_("antigravity.detail.references")}</dt><dd>{detail.summary.credential_reference_count}</dd></div>
				</dl>
				{#if detail.summary.warnings?.length}
					<Alert.Root><TriangleAlertIcon data-icon="inline-start" /><Alert.Title>{$_("antigravity.notice.warningTitle")}</Alert.Title><Alert.Description>{joinUserMessages(detail.summary.warnings, profileWarningMessage)}</Alert.Description></Alert.Root>
				{/if}
			</Card.Content>
			<Card.Footer class="justify-end gap-2">
				<DropdownMenu.Root>
					<DropdownMenu.Trigger>
						{#snippet child({ props })}<Button {...props} variant="outline" size="icon-sm" disabled={!!busyAction} aria-label={$_("actions.more")}><MoreHorizontalIcon /></Button>{/snippet}
					</DropdownMenu.Trigger>
					<DropdownMenu.Content align="end">
						<DropdownMenu.Group>
							<DropdownMenu.Item disabled={!providerReady || !!busyAction} onSelect={openEdit}><PencilIcon />{$_("actions.editDetails")}</DropdownMenu.Item>
							<DropdownMenu.Item variant="destructive" disabled={!!busyAction} onSelect={() => openProfileDelete({ id: detail!.summary.profile.id, name: detail!.summary.profile.name || $_("profile.unnamed") })}><Trash2Icon />{$_("actions.deleteProfile")}</DropdownMenu.Item>
						</DropdownMenu.Group>
					</DropdownMenu.Content>
				</DropdownMenu.Root>
				{#if detail.summary.active}<Button variant="outline" disabled={!sourceReady || !!busyAction} onclick={() => openSaveCurrent(detail!.summary.credential_reference_count)}>{$_("antigravity.actions.updateCurrent")}</Button>{/if}
					<Button disabled={!switchReady || (detail.summary.active && !activeLoginMissing) || !!busyAction || useBuilding || useApplying} onclick={() => openUse({ id: detail!.summary.profile.id, name: detail!.summary.profile.name || $_("profile.unnamed") })}>{$_("actions.useProfile")}</Button>
			</Card.Footer>
		</Card.Root>
		<AntigravityQuotaCard
			quota={detailQuota}
			loading={detailQuotaLoading}
			active={detail.summary.active}
			checkedAtUnixMS={detailQuotaCheck.checkedAtUnixMS}
			checkOutcome={detailQuotaCheck.outcome}
			{nowUnixMS}
			onRefresh={() => { void refreshQuota(detail!.summary.profile.id); }}
		/>
	</div>
{/if}

<UseProfileDialog bind:open={useOpen} profile={useProfile} agent="Antigravity" mode="antigravity" currentProfile={activeProfileID} plan={usePlan} building={useBuilding} applying={useApplying} inlineError={useInlineError} onClose={closeUse} onConfirm={confirmUse} />

<ProfileDeleteDialog bind:open={deleteOpen} profile={deleteTarget} onDeleted={profileDeleted} />

<Dialog.Root bind:open={editOpen}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header><Dialog.Title>{$_("profilePages.edit.title")}</Dialog.Title><Dialog.Description>{$_("antigravity.edit.description")}</Dialog.Description></Dialog.Header>
		<Field.FieldGroup>
			<Field.Field data-invalid={!!validateOptionalName(editName)}><Field.FieldLabel for="edit-antigravity-name">{$_("profilePages.form.name")}</Field.FieldLabel><Input id="edit-antigravity-name" bind:value={editName} aria-invalid={!!validateOptionalName(editName)} /></Field.Field>
			<Field.Field data-invalid={!!validateDescription(editDescription)}><Field.FieldLabel for="edit-antigravity-description">{$_("profilePages.form.description")}</Field.FieldLabel><Textarea id="edit-antigravity-description" bind:value={editDescription} aria-invalid={!!validateDescription(editDescription)} rows={3} /></Field.Field>
		</Field.FieldGroup>
		<Dialog.Footer><Button variant="outline" onclick={() => (editOpen = false)}>{$_("actions.cancel")}</Button><Button disabled={!editName.trim() || !!validateOptionalName(editName) || !!validateDescription(editDescription) || busyAction === "antigravity-edit"} onclick={saveMetadata}>{#if busyAction === "antigravity-edit"}<Spinner data-icon="inline-start" />{/if}{$_("actions.save")}</Button></Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog.Root bind:open={saveCurrentOpen}>
	<AlertDialog.Content>
		<AlertDialog.Header>
			{#if saveCurrentReferenceCount > 1}
				<AlertDialog.Title>{$_("antigravity.saveCurrent.sharedTitle")}</AlertDialog.Title>
				<AlertDialog.Description>{$_("antigravity.saveCurrent.sharedDescription", { values: { count: saveCurrentReferenceCount } })}</AlertDialog.Description>
			{:else}
				<AlertDialog.Title>{$_("antigravity.saveCurrent.title")}</AlertDialog.Title>
				<AlertDialog.Description>{$_("antigravity.saveCurrent.description")}</AlertDialog.Description>
			{/if}
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel disabled={busyAction === "antigravity-save-current"}>{$_("actions.cancel")}</AlertDialog.Cancel>
			<AlertDialog.Action disabled={!sourceReady || busyAction === "antigravity-save-current"} onclick={saveCurrent}>
				{#if busyAction === "antigravity-save-current"}<Spinner data-icon="inline-start" />{/if}
				{$_("antigravity.actions.updateCurrent")}
			</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>
