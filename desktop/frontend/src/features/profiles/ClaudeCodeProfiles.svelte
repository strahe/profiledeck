<script lang="ts">
	import { onMount, tick } from "svelte";
	import type { CancellablePromise } from "@wailsio/runtime";
	import { push } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
	import CheckIcon from "@lucide/svelte/icons/check";
	import KeyRoundIcon from "@lucide/svelte/icons/key-round";
	import PlusIcon from "@lucide/svelte/icons/plus";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import { ClaudeCodeService, SwitchService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type { CreateClaudeCodeProfileRequest, UpdateClaudeCodeProfileRequest } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		ClaudeCodeDetectResult, ClaudeCodeProfileDetail, ClaudeCodeProfileSummary,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/claudecode/models";
	import type { SwitchPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/switching/models";
	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import PageHeader from "$lib/components/app/PageHeader.svelte";
	import StatusBadge from "$lib/components/app/StatusBadge.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import * as Card from "$lib/components/ui/card";
	import * as Dialog from "$lib/components/ui/dialog";
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
	import { currentDesktopLocale, translate } from "$lib/i18n";
	import UseProfileDialog from "./UseProfileDialog.svelte";
	import type { ClaudeCodeProfileRoute, ProfileUseRequest, SwitchProfileItem } from "./types";

	interface Props {
		route: ClaudeCodeProfileRoute;
		profiles: ClaudeCodeProfileSummary[];
		detectResult: ClaudeCodeDetectResult | null;
		detectError: string;
		activeProfileID: string;
		loadingProfiles: boolean;
		profileError: string;
		useRequest: ProfileUseRequest | null;
		refreshDetect: () => Promise<ClaudeCodeDetectResult | null>;
		authorizeKeychain: () => Promise<ClaudeCodeDetectResult | null>;
		refreshProfiles: () => Promise<void>;
		onUseRequestHandled: (sequence: number) => void;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	}

	let {
		route, profiles, detectResult, detectError, activeProfileID, loadingProfiles, profileError,
		useRequest, refreshDetect, authorizeKeychain, refreshProfiles, onUseRequestHandled, showError, showNotice,
	}: Props = $props();

	const providerID = "claude-code";
	const inFlight = new Map<string, CancellablePromise<unknown>>();
	let busyAction = $state("");
	let detail = $state<ClaudeCodeProfileDetail | null>(null);
	let detailLoading = $state(false);
	let detailError = $state("");
	let routeKey = "";
	let routeSequence = 0;
	let profileID = $state("");
	let profileName = $state("");
	let profileDescription = $state("");
	let formSubmitted = $state(false);
	let editOpen = $state(false);
	let editName = $state("");
	let editDescription = $state("");
	let saveCurrentOpen = $state(false);
	let saveCurrentReferenceCount = $state(1);
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
	let providerReady = $derived(!!detectResult?.profiledeck_initialized && detectResult.provider_enabled && detectResult.provider_compatible);
	let sourceReady = $derived(providerReady && ["valid", "expiring", "expired"].includes(detectResult?.credential_status || ""));
	let keychainAuthorizationRequired = $derived(providerReady && !!detectResult?.keychain_authorization_required);
	let hasDetectWarning = $derived(providerReady && !!detectResult?.warnings?.length && !["invalid", "unsupported", "unavailable"].includes(detectResult?.credential_status || ""));
	let observedOverrideNames = $derived((detectResult?.observed_auth_override_hints ?? []).join(", "));
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

	onMount(() => () => cancelAll());

	async function enterRoute(next: ClaudeCodeProfileRoute) {
		const sequence = ++routeSequence;
		cancelAll();
		closeUse();
		detailError = "";
		formSubmitted = false;
		editOpen = false;
		if (next.kind === "list" || next.kind === "new") {
			detail = null;
			if (next.kind === "new") resetForm();
			await Promise.all([refreshDetect(), refreshProfiles()]);
			return;
		}
		await Promise.all([loadDetail(next.profileID, sequence), refreshDetect(), refreshProfiles()]);
	}

	async function loadDetail(id: string, sequence = routeSequence) {
		detailLoading = true;
		try {
			const result = await track("claude-code-detail", ClaudeCodeService.ShowProfile(id));
			if (sequence === routeSequence) detail = result;
		} catch (error) {
			if (sequence === routeSequence && !isCancelError(error)) detailError = formatError(error);
		} finally {
			if (sequence === routeSequence) detailLoading = false;
		}
	}

	function resetForm() { profileID = ""; profileName = ""; profileDescription = ""; }

	async function createProfile() {
		formSubmitted = true;
		if (rawIDError || rawNameError || rawDescriptionError) return;
		await runAction("claude-code-create", async () => {
			if (!isSourceReady(await refreshDetect())) return;
			const request: CreateClaudeCodeProfileRequest = {
				profile_id: profileID.trim(), name: optional(profileName), description: optional(profileDescription),
			};
			const result = await track("claude-code-create", ClaudeCodeService.CreateProfile(request));
			await refreshProfiles();
			showNotice(translate("claudeCode.notice.createdTitle"), translate("claudeCode.notice.createdDescription", { profile: result.summary.profile.name || result.summary.profile.id }));
			await push(`/claude-code/profiles/${encodeURIComponent(result.summary.profile.id)}`);
		});
	}

	function openEdit() {
		if (!detail) return;
		editName = detail.summary.profile.name || detail.summary.profile.id;
		editDescription = detail.summary.profile.description || "";
		editOpen = true;
	}

	async function saveMetadata() {
		if (!detail || !editName.trim() || validateOptionalName(editName) || validateDescription(editDescription)) return;
		await runAction("claude-code-edit", async () => {
			const request: UpdateClaudeCodeProfileRequest = {
				profile_id: detail!.summary.profile.id, name: editName.trim(), description: editDescription.trim(),
			};
			detail = await track("claude-code-edit", ClaudeCodeService.UpdateProfile(request));
			editOpen = false;
			await refreshProfiles();
			showNotice(translate("claudeCode.notice.updatedTitle"), translate("claudeCode.notice.updatedDescription"));
		});
	}

	function openSaveCurrent(referenceCount: number) {
		saveCurrentReferenceCount = Math.max(1, referenceCount);
		saveCurrentOpen = true;
	}

	async function saveCurrent() {
		await runAction("claude-code-save-current", async () => {
			if (!isSourceReady(await refreshDetect())) return;
			await track("claude-code-save-current", ClaudeCodeService.SaveCurrent(saveCurrentReferenceCount > 1));
			saveCurrentOpen = false;
			await Promise.all([refreshProfiles(), detail ? loadDetail(detail.summary.profile.id) : Promise.resolve()]);
			showNotice(translate("claudeCode.notice.savedTitle"), translate("claudeCode.notice.savedDescription"));
		});
	}

	async function openUse(profile: SwitchProfileItem) {
		closeUse();
		const sequence = useSequence;
		useProfile = profile;
		useOpen = true;
		useBuilding = true;
		try {
			const plan = await track("claude-code-use-build", SwitchService.BuildPlan(providerID, profile.id));
			if (sequence === useSequence) usePlan = plan;
		} catch (error) {
			if (sequence === useSequence && !isCancelError(error)) useInlineError = formatError(error);
		} finally {
			if (sequence === useSequence) useBuilding = false;
		}
	}

	async function openUseByProfileID(id: string) {
		let profile = listItems.find((item) => item.id === id);
		if (!profile) { await refreshProfiles(); await tick(); profile = listItems.find((item) => item.id === id); }
		if (profile) await openUse(profile); else showError({ code: "PROFILE_NOT_FOUND" });
	}

	async function confirmUse() {
		if (useApplying || !useProfile || !usePlan?.plan_fingerprint) return;
		const sequence = useSequence;
		const switchedProfileID = useProfile.id;
		useApplying = true;
		try {
			const result = await track("claude-code-use-apply", SwitchService.Apply({
				provider_id: providerID, profile_id: useProfile.id,
				expected_plan_fingerprint: usePlan.plan_fingerprint, confirm: true,
			}));
			if (sequence !== useSequence) return;
			closeUse();
			await Promise.all([
				refreshProfiles(),
				refreshDetect(),
				route.kind === "detail" && route.profileID === switchedProfileID ? loadDetail(switchedProfileID) : Promise.resolve(),
			]);
			if (!result.recovery_cleanup_completed) {
				toast.warning(translate("notice.recoveryCleanup.switchAppliedTitle"), {
					description: translate("notice.recoveryCleanup.switchAppliedDescription", { profile: result.profile.name || result.profile.id }),
				});
			} else {
				showNotice(translate("claudeCode.notice.switchedTitle"), translate("claudeCode.notice.switchedDescription", { profile: result.profile.name || result.profile.id }));
			}
		} catch (error) {
			if (sequence !== useSequence || isCancelError(error)) return;
			if (isDesktopErrorCode(error, "TARGET_CHANGED")) {
				useInlineError = translate("claudeCode.use.changed");
				usePlan = await track("claude-code-use-build", SwitchService.BuildPlan(providerID, useProfile.id));
			} else { useInlineError = formatError(error); showError(error); }
		} finally {
			if (sequence === useSequence) useApplying = false;
		}
	}

	function closeUse() {
		useSequence += 1;
		cancelAction("claude-code-use-build"); cancelAction("claude-code-use-apply");
		useOpen = false; useProfile = null; usePlan = null; useBuilding = false; useApplying = false; useInlineError = "";
	}

	async function runAction(name: string, action: () => Promise<void>) {
		if (busyAction) return;
		busyAction = name;
		try { await action(); } catch (error) { if (!isCancelError(error)) showError(error); }
		finally { if (busyAction === name) busyAction = ""; }
	}

	async function handleSourceAction() {
		await runAction("claude-code-source-action", async () => {
			if (keychainAuthorizationRequired) await authorizeKeychain();
			else await refreshDetect();
		});
	}

	function track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		cancelAction(key); inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => { if (inFlight.get(key) === promise) inFlight.delete(key); }).catch(() => {});
		return promise;
	}
	function cancelAction(key: string) { inFlight.get(key)?.cancel("replaced"); inFlight.delete(key); }
	function cancelAll() { for (const promise of inFlight.values()) promise.cancel("route-change"); inFlight.clear(); }
	function isSourceReady(value: ClaudeCodeDetectResult | null | undefined) { return !!value?.profiledeck_initialized && value.provider_enabled && value.provider_compatible && ["valid", "expiring", "expired"].includes(value.credential_status); }
	function sourceDescription() {
		if (!detectResult && !detectError) return translate("claudeCode.source.checking");
		if (!detectResult && detectError) return detectError;
		if (detectResult && !detectResult.profiledeck_initialized) return translate("claudeCode.source.notInitialized");
		if (detectResult && !detectResult.provider_compatible) return translate("claudeCode.source.incompatible");
		if (detectResult?.provider_exists && !detectResult.provider_enabled) return translate("claudeCode.source.disabled");
		if (keychainAuthorizationRequired) return translate("claudeCode.source.authorizationRequired");
		return translate(`claudeCode.source.${detectResult?.credential_status || "missing"}`);
	}
	function formatExpiry(value: number | undefined) { return value ? new Date(value).toLocaleString(currentDesktopLocale(), { dateStyle: "medium", timeStyle: "short" }) : translate("claudeCode.detail.unknownExpiry"); }
	function validateProfileID(value: string) { const text = value.trim(); if (!text) return translate("profilePages.validation.idRequired"); if (text.length > 80) return translate("profilePages.validation.idTooLong"); return /^[a-z0-9][a-z0-9._-]*$/.test(text) ? "" : translate("profilePages.validation.idFormat"); }
	function validateOptionalName(value: string) { return value.trim().length > 120 ? translate("profilePages.validation.nameTooLong") : ""; }
	function validateDescription(value: string) { return value.trim().length > 1000 ? translate("profilePages.validation.descriptionTooLong") : ""; }
	function optional(value: string): string | null { return value.trim() || null; }
	function formatError(value: unknown) { return desktopErrorMessage(value, translate("errors.desktopUnavailable")); }
</script>

{#if route.kind === "list"}
	<ContentContainer>
		<PageHeader title={$_("claudeCode.list.title")} description={$_("claudeCode.list.description")}>
			{#snippet actions()}<Button disabled={!sourceReady || !!busyAction} onclick={() => push("/claude-code/profiles/new")}><PlusIcon data-icon="inline-start" />{$_("claudeCode.actions.newProfile")}</Button>{/snippet}
		</PageHeader>
		{#if (detectResult !== null || !!detectError) && !sourceReady}
			<Alert.Root variant={["invalid", "unsupported"].includes(detectResult?.credential_status || "") ? "destructive" : "default"}>
				<TriangleAlertIcon data-icon="inline-start" /><Alert.Title>{$_("claudeCode.source.notReadyTitle")}</Alert.Title><Alert.Description>{sourceDescription()}</Alert.Description>
				<Alert.Action><Button size="xs" variant="outline" disabled={!!busyAction} onclick={() => { void handleSourceAction(); }}>{#if busyAction === "claude-code-source-action"}<Spinner data-icon="inline-start" />{/if}{keychainAuthorizationRequired ? $_("claudeCode.source.authorizeAction") : $_("actions.retry")}</Button></Alert.Action>
			</Alert.Root>
		{/if}
		{#if hasDetectWarning}<Alert.Root><TriangleAlertIcon /><Alert.Title>{$_("claudeCode.source.warningTitle")}</Alert.Title><Alert.Description>{$_("claudeCode.source.warningDescription")}</Alert.Description></Alert.Root>{/if}
		{#if observedOverrideNames}<Alert.Root><TriangleAlertIcon /><Alert.Title>{$_("claudeCode.source.overrideTitle")}</Alert.Title><Alert.Description>{$_("claudeCode.source.overrideDescription", { values: { names: observedOverrideNames } })}</Alert.Description></Alert.Root>{/if}
		<Card.Root size="sm"><Card.Content class="px-0">
			{#if loadingProfiles}<div class="flex flex-col gap-3 px-4 py-4"><Skeleton class="h-12 w-full" /><Skeleton class="h-12 w-full" /></div>
			{:else if profileError}<div class="p-4"><Alert.Root variant="destructive"><TriangleAlertIcon /><Alert.Title>{$_("profilePages.errorTitle")}</Alert.Title><Alert.Description>{profileError}</Alert.Description></Alert.Root></div>
			{:else if profiles.length === 0}<Empty.Root><Empty.Header><Empty.Title>{$_("claudeCode.list.emptyTitle")}</Empty.Title><Empty.Description>{$_("claudeCode.list.emptyDescription")}</Empty.Description></Empty.Header>{#if sourceReady}<Empty.Content><Button onclick={() => push("/claude-code/profiles/new")}><PlusIcon />{$_("claudeCode.actions.newProfile")}</Button></Empty.Content>{/if}</Empty.Root>
			{:else}{#each profiles as summary, index (summary.profile.id)}
				<div class="flex flex-col gap-2 px-4 py-3">
					<div class="flex items-center gap-3"><button class="min-w-0 flex-1 truncate text-left font-medium hover:underline" onclick={() => push(`/claude-code/profiles/${encodeURIComponent(summary.profile.id)}`)}>{summary.profile.name || summary.profile.id}</button>
						{#if summary.active}<StatusBadge tone="current"><CheckIcon />{$_("status.current")}</StatusBadge>{/if}<StatusBadge tone={summary.credential_status === "valid" ? "success" : "warning"}>{$_(`claudeCode.status.${summary.credential_status}`)}</StatusBadge>
						{#if !summary.active}<Button size="sm" disabled={!providerReady || !!busyAction} onclick={() => openUse({ id: summary.profile.id, name: summary.profile.name || summary.profile.id })}>{$_("actions.useProfile")}</Button>{/if}
					</div><div class="text-sm text-muted-foreground">{$_("claudeCode.detail.expiry")}: {formatExpiry(summary.expires_at_unix_ms)}</div>
					{#if summary.warnings?.length}<Alert.Root><TriangleAlertIcon /><Alert.Description>{$_("claudeCode.notice.profileWarning")}</Alert.Description></Alert.Root>{/if}
				</div>{#if index < profiles.length - 1}<Separator />{/if}
			{/each}{/if}
		</Card.Content></Card.Root>
	</ContentContainer>
{:else if route.kind === "new"}
	<div class="mx-auto flex w-full max-w-3xl flex-col gap-4"><Button class="self-start" variant="ghost" onclick={() => push("/claude-code/profiles")}><ArrowLeftIcon />{$_("actions.backToProfiles")}</Button>
		<Card.Root><Card.Header><Card.Title>{$_("claudeCode.new.title")}</Card.Title><Card.Description>{$_("claudeCode.new.description")}</Card.Description></Card.Header><Card.Content class="flex flex-col gap-4">
			<Alert.Root variant={sourceReady || (!detectResult && !detectError) || keychainAuthorizationRequired ? "default" : "destructive"}><KeyRoundIcon /><Alert.Title>{sourceReady ? $_("claudeCode.source.readyTitle") : $_("claudeCode.source.notReadyTitle")}</Alert.Title><Alert.Description>{sourceDescription()}</Alert.Description>{#if !sourceReady && (detectResult !== null || !!detectError)}<Alert.Action><Button size="xs" variant="outline" disabled={!!busyAction} onclick={() => { void handleSourceAction(); }}>{#if busyAction === "claude-code-source-action"}<Spinner data-icon="inline-start" />{/if}{keychainAuthorizationRequired ? $_("claudeCode.source.authorizeAction") : $_("actions.retry")}</Button></Alert.Action>{/if}</Alert.Root>
			{#if hasDetectWarning}<Alert.Root><TriangleAlertIcon /><Alert.Title>{$_("claudeCode.source.warningTitle")}</Alert.Title><Alert.Description>{$_("claudeCode.source.warningDescription")}</Alert.Description></Alert.Root>{/if}
			{#if observedOverrideNames}<Alert.Root><TriangleAlertIcon /><Alert.Title>{$_("claudeCode.source.overrideTitle")}</Alert.Title><Alert.Description>{$_("claudeCode.source.overrideDescription", { values: { names: observedOverrideNames } })}</Alert.Description></Alert.Root>{/if}
			<Field.FieldGroup><Field.Field data-invalid={!!displayedIDError}><Field.FieldLabel for="claude-code-profile-id">{$_("profilePages.form.profileID")}</Field.FieldLabel><Input id="claude-code-profile-id" bind:value={profileID} aria-invalid={!!displayedIDError} />{#if displayedIDError}<Field.FieldError>{displayedIDError}</Field.FieldError>{/if}</Field.Field>
			<Field.Field data-invalid={!!displayedNameError}><Field.FieldLabel for="claude-code-profile-name">{$_("profilePages.form.name")}</Field.FieldLabel><Input id="claude-code-profile-name" bind:value={profileName} aria-invalid={!!displayedNameError} />{#if displayedNameError}<Field.FieldError>{displayedNameError}</Field.FieldError>{/if}</Field.Field>
			<Field.Field data-invalid={!!displayedDescriptionError}><Field.FieldLabel for="claude-code-profile-description">{$_("profilePages.form.description")}</Field.FieldLabel><Textarea id="claude-code-profile-description" bind:value={profileDescription} rows={3} />{#if displayedDescriptionError}<Field.FieldError>{displayedDescriptionError}</Field.FieldError>{/if}</Field.Field></Field.FieldGroup>
		</Card.Content><Card.Footer class="justify-end gap-2"><Button variant="outline" onclick={() => push("/claude-code/profiles")}>{$_("actions.cancel")}</Button><Button disabled={!sourceReady || !!busyAction} onclick={createProfile}>{#if busyAction === "claude-code-create"}<Spinner />{/if}{$_("claudeCode.actions.saveProfile")}</Button></Card.Footer></Card.Root>
	</div>
{:else if detailLoading}<div class="mx-auto w-full max-w-3xl"><Skeleton class="h-72 w-full" /></div>
{:else if detailError || !detail}<div class="mx-auto w-full max-w-3xl"><Alert.Root variant="destructive"><TriangleAlertIcon /><Alert.Title>{$_("profilePages.errorTitle")}</Alert.Title><Alert.Description>{detailError}</Alert.Description></Alert.Root></div>
{:else}
	<div class="mx-auto flex w-full max-w-3xl flex-col gap-4"><Button class="self-start" variant="ghost" onclick={() => push("/claude-code/profiles")}><ArrowLeftIcon />{$_("actions.backToProfiles")}</Button>
		<Card.Root><Card.Header><Card.Title>{detail.summary.profile.name || detail.summary.profile.id}</Card.Title><Card.Description>{detail.summary.profile.description || $_("profile.noDescription")}</Card.Description>{#if detail.summary.active}<Card.Action><Badge>{$_("status.current")}</Badge></Card.Action>{/if}</Card.Header>
			<Card.Content><dl class="grid gap-4 text-sm sm:grid-cols-2"><div><dt class="text-muted-foreground">{$_("profilePages.form.profileID")}</dt><dd class="font-mono">{detail.summary.profile.id}</dd></div><div><dt class="text-muted-foreground">{$_("claudeCode.detail.status")}</dt><dd>{$_(`claudeCode.status.${detail.summary.credential_status}`)}</dd></div><div><dt class="text-muted-foreground">{$_("claudeCode.detail.expiry")}</dt><dd>{formatExpiry(detail.summary.expires_at_unix_ms)}</dd></div><div><dt class="text-muted-foreground">{$_("claudeCode.detail.references")}</dt><dd>{detail.summary.credential_reference_count}</dd></div></dl>
			{#if detail.summary.warnings?.length}<Alert.Root class="mt-4"><TriangleAlertIcon /><Alert.Description>{$_("claudeCode.notice.profileWarning")}</Alert.Description></Alert.Root>{/if}</Card.Content>
			<Card.Footer class="justify-end gap-2"><Button variant="outline" onclick={openEdit}>{$_("actions.editDetails")}</Button>{#if detail.summary.active}<Button variant="outline" disabled={!sourceReady || !!busyAction} onclick={() => openSaveCurrent(detail!.summary.credential_reference_count)}><RefreshCwIcon />{$_("claudeCode.actions.updateCurrent")}</Button>{/if}<Button disabled={!providerReady || detail.summary.active || !!busyAction} onclick={() => openUse({ id: detail!.summary.profile.id, name: detail!.summary.profile.name || detail!.summary.profile.id })}>{$_("actions.useProfile")}</Button></Card.Footer>
		</Card.Root>
	</div>
{/if}

<UseProfileDialog bind:open={useOpen} profile={useProfile} agent="Claude Code" mode="claude-code" currentProfile={activeProfileID} plan={usePlan} building={useBuilding} applying={useApplying} inlineError={useInlineError} onClose={closeUse} onConfirm={confirmUse} />

<Dialog.Root bind:open={editOpen}><Dialog.Content class="sm:max-w-lg"><Dialog.Header><Dialog.Title>{$_("profilePages.edit.title")}</Dialog.Title><Dialog.Description>{$_("claudeCode.edit.description")}</Dialog.Description></Dialog.Header><Field.FieldGroup><Field.Field><Field.FieldLabel for="edit-claude-code-name">{$_("profilePages.form.name")}</Field.FieldLabel><Input id="edit-claude-code-name" bind:value={editName} /></Field.Field><Field.Field><Field.FieldLabel for="edit-claude-code-description">{$_("profilePages.form.description")}</Field.FieldLabel><Textarea id="edit-claude-code-description" bind:value={editDescription} rows={3} /></Field.Field></Field.FieldGroup><Dialog.Footer><Button variant="outline" onclick={() => (editOpen = false)}>{$_("actions.cancel")}</Button><Button disabled={!editName.trim() || !!busyAction} onclick={saveMetadata}>{#if busyAction === "claude-code-edit"}<Spinner />{/if}{$_("actions.save")}</Button></Dialog.Footer></Dialog.Content></Dialog.Root>

<AlertDialog.Root bind:open={saveCurrentOpen}><AlertDialog.Content><AlertDialog.Header>{#if saveCurrentReferenceCount > 1}<AlertDialog.Title>{$_("claudeCode.saveCurrent.sharedTitle")}</AlertDialog.Title><AlertDialog.Description>{$_("claudeCode.saveCurrent.sharedDescription", { values: { count: saveCurrentReferenceCount } })}</AlertDialog.Description>{:else}<AlertDialog.Title>{$_("claudeCode.saveCurrent.title")}</AlertDialog.Title><AlertDialog.Description>{$_("claudeCode.saveCurrent.description")}</AlertDialog.Description>{/if}</AlertDialog.Header><AlertDialog.Footer><AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel><AlertDialog.Action disabled={!sourceReady || !!busyAction} onclick={saveCurrent}>{#if busyAction === "claude-code-save-current"}<Spinner />{/if}{$_("claudeCode.actions.updateCurrent")}</AlertDialog.Action></AlertDialog.Footer></AlertDialog.Content></AlertDialog.Root>
