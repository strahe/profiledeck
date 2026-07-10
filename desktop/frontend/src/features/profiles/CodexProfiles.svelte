<script lang="ts">
	import { onDestroy, tick } from "svelte";
	import { type CancellablePromise } from "@wailsio/runtime";
	import { push } from "svelte-spa-router";
	import { _, locale } from "svelte-i18n";
	import { toast } from "svelte-sonner";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import ArrowLeftIcon from "@lucide/svelte/icons/arrow-left";
	import CheckCircleIcon from "@lucide/svelte/icons/circle-check";
	import GitForkIcon from "@lucide/svelte/icons/git-fork";
	import RefreshCwIcon from "@lucide/svelte/icons/refresh-cw";
	import SaveIcon from "@lucide/svelte/icons/save";

	import { CodexService, SwitchService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CreateCodexProfileRequest,
		ForkCodexProfileRequest,
		SyncCodexProfileRequest,
		UpdateCodexProfileMetadataRequest,
	} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
	import type {
		CodexDetectResult,
		CodexProfileDetail,
		CodexProfileSaveResult,
		CodexProfileSummary,
		SwitchPlan,
	} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";

	import * as Alert from "$lib/components/ui/alert";
	import * as Breadcrumb from "$lib/components/ui/breadcrumb";
	import * as Card from "$lib/components/ui/card";
	import * as Dialog from "$lib/components/ui/dialog";
	import * as Field from "$lib/components/ui/field";
	import * as RadioGroup from "$lib/components/ui/radio-group";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Input } from "$lib/components/ui/input";
	import { Separator } from "$lib/components/ui/separator";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { Textarea } from "$lib/components/ui/textarea";
	import { desktopErrorCode, desktopErrorDetails, desktopErrorMessage, isCancelError, isDesktopErrorCode } from "$lib/desktop-errors";
	import { currentDesktopLocale, translate } from "$lib/i18n";

	import ProfileDetail from "./ProfileDetail.svelte";
	import ProfileForm from "./ProfileForm.svelte";
	import ProfileList from "./ProfileList.svelte";
	import UseProfileDialog from "./UseProfileDialog.svelte";
	import type {
		CodexForkAuthBinding,
		CodexProfileListItem,
		CodexProfileRoute,
		CodexSyncAuthUpdate,
		ProfileUseRequest,
	} from "./types";

	let {
		route,
		profiles,
		detectResult,
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
	}: {
		route: CodexProfileRoute;
		profiles: CodexProfileSummary[];
		detectResult: CodexDetectResult | null;
		activeProfileID: string;
		loadingProfiles: boolean;
		profileError: string;
		useRequest: ProfileUseRequest | null;
		refreshDetect: () => Promise<void>;
		refreshProfiles: () => Promise<void>;
		cancelDetect: () => void;
		onUseRequestHandled: (sequence: number) => void;
		showError: (value: unknown) => void;
		showNotice: (title: string, description: string) => void;
	} = $props();

	const codexProviderID = "codex";
	const inFlight = new Map<string, CancellablePromise<unknown>>();

	let busyAction = $state("");
	let detail = $state<CodexProfileDetail | null>(null);
	let detailLoading = $state(false);
	let detailError = $state("");
	let lastRouteKey = "";
	let detailSequence = 0;
	let sourceDetecting = $state(false);

	let profileID = $state("");
	let profileName = $state("");
	let profileDescription = $state("");
	let formSubmitted = $state(false);
	let forkAuthBinding = $state<CodexForkAuthBinding>("share-parent");

	let editOpen = $state(false);
	let editName = $state("");
	let editDescription = $state("");
	let editSubmitted = $state(false);

	let syncOpen = $state(false);
	let syncStage = $state<"confirm" | "conflict">("confirm");
	let syncAuthUpdate = $state<CodexSyncAuthUpdate | "">("");
	let syncError = $state("");
	let syncDetecting = $state(false);

	let useOpen = $state(false);
	let useProfile = $state<CodexProfileListItem | null>(null);
	let usePlan = $state<SwitchPlan | null>(null);
	let useBuilding = $state(false);
	let useApplying = $state(false);
	let useInlineError = $state("");
	let handledUseSequence = 0;
	let useSequence = 0;

	let listItems = $derived.by(() => {
		void $locale;
		return profiles.map(profileListItem);
	});
	let sourceReady = $derived(
		!!detectResult?.profiledeck_initialized &&
			detectResult.provider_compatible &&
			detectResult.config_status === "valid" &&
			detectResult.auth_status === "valid",
	);
	let rawIDError = $derived.by(() => {
		void $locale;
		return validateProfileID(profileID);
	});
	let rawNameError = $derived.by(() => {
		void $locale;
		return validateOptionalName(profileName);
	});
	let rawDescriptionError = $derived.by(() => {
		void $locale;
		return validateDescription(profileDescription);
	});
	let formValid = $derived(!rawIDError && !rawNameError && !rawDescriptionError);
	let displayedIDError = $derived(formSubmitted || profileID ? rawIDError : "");
	let displayedNameError = $derived(formSubmitted || profileName ? rawNameError : "");
	let displayedDescriptionError = $derived(formSubmitted || profileDescription ? rawDescriptionError : "");
	let editNameError = $derived.by(() => {
		void $locale;
		return editSubmitted ? validateRequiredName(editName) : "";
	});
	let editDescriptionError = $derived.by(() => {
		void $locale;
		return editSubmitted ? validateDescription(editDescription) : "";
	});

	$effect(() => {
		const key = `${route.kind}:${route.profileID}`;
		if (key === lastRouteKey) return;
		lastRouteKey = key;
		void enterRoute(route);
	});

	$effect(() => {
		if (!useRequest || useRequest.sequence === handledUseSequence) return;
		handledUseSequence = useRequest.sequence;
		void openUseByProfileID(useRequest.profileID).finally(() => onUseRequestHandled(useRequest.sequence));
	});

	onDestroy(() => {
		cancelAll();
		cancelDetect();
	});

	async function enterRoute(next: CodexProfileRoute) {
		const sequence = ++detailSequence;
		for (const key of ["profile-detail", "profile-create", "profile-fork", "profile-metadata", "profile-sync"]) cancelAction(key);
		cancelDetect();
		if (useOpen) closeUse();
		editOpen = false;
		syncOpen = false;
		syncStage = "confirm";
		syncAuthUpdate = "";
		syncError = "";
		busyAction = "";
		sourceDetecting = false;
		detailError = "";
		formSubmitted = false;
		if (next.kind === "list") {
			detail = null;
			resetForm();
			return;
		}
		if (next.kind === "new") {
			detail = null;
			resetForm();
			await detectSource(sequence);
			return;
		}
		await loadDetail(next.profileID, next.kind === "fork", sequence);
	}

	async function detectSource(sequence = detailSequence) {
		sourceDetecting = true;
		try {
			await refreshDetect();
		} finally {
			if (sequence === detailSequence) sourceDetecting = false;
		}
	}

	async function loadDetail(id: string, prepareFork = false, sequence = detailSequence) {
		detailLoading = true;
		detailError = "";
		try {
			const next = await track("profile-detail", CodexService.ShowProfile(id));
			if (sequence !== detailSequence) return;
			detail = next;
			if (prepareFork) {
				const sourceName = next.summary.profile.name || next.summary.profile.id;
				profileID = `${next.summary.profile.id}-copy`;
				profileName = translate("profilePages.fork.copyName", { profile: sourceName });
				profileDescription = next.summary.profile.description || "";
				forkAuthBinding = "share-parent";
			}
		} catch (error) {
			if (sequence === detailSequence && !isCancelError(error)) detailError = formatError(error);
		} finally {
			if (sequence === detailSequence) detailLoading = false;
		}
	}

	function resetForm() {
		profileID = "";
		profileName = "";
		profileDescription = "";
		forkAuthBinding = "share-parent";
		formSubmitted = false;
	}

	async function createProfile() {
		formSubmitted = true;
		if (!formValid || !sourceReady) return;
		const request: CreateCodexProfileRequest = {
			profile_id: profileID.trim(),
			name: optional(profileName),
			description: optional(profileDescription),
		};
		await runAction("profile-create", async () => {
			const result = await track("profile-create", CodexService.CreateProfile(request));
			await refreshProfiles();
			showResultWarnings(result);
			showNotice(
				translate("notice.profileCreated.title"),
				translate("notice.profileCreated.codexDescription", { profile: result.profile.name || result.profile.id }),
			);
			await push(`/codex/profiles/${encodeURIComponent(result.profile.id)}`);
		});
	}

	async function forkProfile() {
		formSubmitted = true;
		if (!formValid || !detail) return;
		const request: ForkCodexProfileRequest = {
			source_profile_id: detail.summary.profile.id,
			profile_id: profileID.trim(),
			auth_binding: forkAuthBinding,
			name: optional(profileName),
			description: optional(profileDescription),
		};
		await runAction("profile-fork", async () => {
			const result = await track("profile-fork", CodexService.ForkProfile(request));
			await refreshProfiles();
			showResultWarnings(result);
			showNotice(
				translate("notice.profileForked.title"),
				translate("notice.profileForked.codexDescription", { profile: result.profile.name || result.profile.id }),
			);
			await push(`/codex/profiles/${encodeURIComponent(result.profile.id)}`);
		});
	}

	function openEdit() {
		if (!detail) return;
		editName = detail.summary.profile.name || detail.summary.profile.id;
		editDescription = detail.summary.profile.description || "";
		editSubmitted = false;
		editOpen = true;
	}

	async function saveMetadata() {
		if (!detail) return;
		editSubmitted = true;
		if (validateRequiredName(editName) || validateDescription(editDescription)) return;

		const current = detail.summary.profile;
		const nextName = editName.trim();
		const nextDescription = editDescription.trim();
		const request: UpdateCodexProfileMetadataRequest = { profile_id: current.id };
		if (nextName !== current.name) request.name = nextName;
		if (nextDescription !== current.description) request.description = nextDescription;
		if (request.name === undefined && request.description === undefined) {
			editOpen = false;
			return;
		}

		await runAction("profile-metadata", async () => {
			const updated = await track("profile-metadata", CodexService.UpdateProfileMetadata(request));
			if (detail) {
				detail = { ...detail, summary: { ...detail.summary, profile: updated, updated_at_unix_ms: updated.updated_at_unix_ms } };
			}
			editOpen = false;
			await refreshProfiles();
			showNotice(translate("notice.profileUpdated.title"), translate("notice.profileUpdated.description"));
		});
	}

	async function openSync() {
		if (!detail) return;
		syncStage = "confirm";
		syncAuthUpdate = "";
		syncError = "";
		syncOpen = true;
		syncDetecting = true;
		try {
			await refreshDetect();
		} finally {
			if (syncOpen) syncDetecting = false;
		}
	}

	function handleSyncOpenChange(value: boolean) {
		syncOpen = value;
		if (value) return;
		cancelDetect();
		cancelAction("profile-sync");
		syncStage = "confirm";
		syncAuthUpdate = "";
		syncError = "";
		syncDetecting = false;
		if (busyAction === "profile-sync") busyAction = "";
	}

	async function syncProfile() {
		if (!detail || syncDetecting) return;
		if (syncStage === "confirm" && !sourceReady) {
			syncError = translate("profilePages.sync.sourceNotReady");
			return;
		}
		if (syncStage === "conflict" && !syncAuthUpdate) return;

		busyAction = "profile-sync";
		syncError = "";
		const request: SyncCodexProfileRequest = {
			profile_id: detail.summary.profile.id,
			auth_update: syncStage === "conflict" ? syncAuthUpdate : undefined,
		};
		try {
			const result = await track("profile-sync", CodexService.SyncProfile(request));
			showResultWarnings(result);
			syncOpen = false;
			await Promise.all([refreshProfiles(), loadDetail(result.profile.id)]);
			showNotice(
				translate("notice.profileSynced.title"),
				translate("notice.profileSynced.codexDescription", { profile: result.profile.name || result.profile.id }),
			);
		} catch (error) {
			if (isCancelError(error)) return;
			if (isSharedCredentialConflict(error)) {
				syncStage = "conflict";
				syncAuthUpdate = "";
				return;
			}
			syncError = formatError(error);
			showError(error);
		} finally {
			if (busyAction === "profile-sync") busyAction = "";
		}
	}

	function isSharedCredentialConflict(error: unknown): boolean {
		if (desktopErrorCode(error) !== "CODEX_INVALID") return false;
		const supported = desktopErrorDetails(error)?.supported_auth_updates;
		return Array.isArray(supported) && supported.includes("update-shared") && supported.includes("fork-new");
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
			if (sequence === useSequence && !isCancelError(error)) {
				useInlineError = formatError(error);
				showError(error);
			}
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
		if (!profile) {
			showError(translate("errors.codexProfileNotFound", { profile: id }));
			return;
		}
		await openUse(profile);
	}

	async function confirmUse() {
		if (!useProfile || !usePlan?.plan_fingerprint) return;
		const sequence = useSequence;
		useApplying = true;
		useInlineError = "";
		const profile = useProfile;
		try {
			const result = await track(
				"use-apply",
				SwitchService.Apply({
					provider_id: codexProviderID,
					profile_id: profile.id,
					expected_plan_fingerprint: usePlan.plan_fingerprint,
					confirm: true,
				}),
			);
			if (sequence !== useSequence) return;
			closeUse();
			await refreshProfiles();
			showNotice(
				translate("notice.profileSwitched.title"),
				translate("notice.profileSwitched.codexDescription", { profile: result.profile.name || result.profile.id }),
			);
		} catch (error) {
			if (sequence !== useSequence || isCancelError(error)) return;
			if (isDesktopErrorCode(error, "TARGET_CHANGED")) {
				useInlineError = translate("errors.targetChanged");
				usePlan = null;
				await rebuildUsePlan(profile, sequence);
				return;
			}
			useInlineError = formatError(error);
			showError(error);
		} finally {
			if (sequence === useSequence) useApplying = false;
		}
	}

	async function rebuildUsePlan(profile: CodexProfileListItem, sequence: number) {
		useBuilding = true;
		try {
			const plan = await track("use-build", SwitchService.BuildPlan(codexProviderID, profile.id));
			if (sequence === useSequence) usePlan = plan;
		} catch (error) {
			if (sequence === useSequence && !isCancelError(error)) {
				useInlineError = `${translate("errors.targetChanged")} ${formatError(error)}`;
			}
		} finally {
			if (sequence === useSequence) useBuilding = false;
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

	function showResultWarnings(result: CodexProfileSaveResult) {
		if (!result.warnings?.length) return;
		toast.warning(translate("notice.profileWarnings.title"), { description: result.warnings.join(" ") });
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
		for (const promise of inFlight.values()) promise.cancel("unmount");
		inFlight.clear();
	}

	function profileListItem(summary: CodexProfileSummary): CodexProfileListItem {
		return {
			summary,
			id: summary.profile.id,
			name: summary.profile.name || summary.profile.id,
			description: summary.profile.description || "",
			updated: formatRelativeTime(summary.updated_at_unix_ms),
			model: summary.model || "",
			provider: summary.model_provider || "",
			baseURL: summary.openai_base_url || "",
			account: summary.codex_account_id || "",
		};
	}

	function formatRelativeTime(value: number | undefined, requestedLocale?: string | null): string {
		if (!value) return "—";
		const locale = requestedLocale === "zh-CN" || requestedLocale === "en-US" ? requestedLocale : currentDesktopLocale();
		const delta = Date.now() - value;
		if (delta < 60_000) return translate("time.justNow");
		if (delta < 3_600_000) return translate("time.minutesAgo", { count: Math.max(1, Math.floor(delta / 60_000)) });
		const date = new Date(value);
		if (date.toDateString() === new Date().toDateString()) {
			return translate("time.todayAt", { time: date.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" }) });
		}
		return date.toLocaleDateString(locale, { month: "short", day: "numeric" });
	}

	function sourceStatusLabel(status: string | undefined, currentLocale?: string | null): string {
		void currentLocale;
		if (status === "valid") return translate("sourceStatus.valid");
		if (status === "invalid") return translate("sourceStatus.invalid");
		if (status === "unreadable") return translate("sourceStatus.unreadable");
		return translate("sourceStatus.missing");
	}

	function sourceStatusVariant(status: string | undefined): "secondary" | "destructive" | "outline" {
		if (status === "valid") return "secondary";
		if (status === "invalid" || status === "unreadable") return "destructive";
		return "outline";
	}

	function validateProfileID(value: string): string {
		const trimmed = value.trim();
		if (!trimmed) return translate("profilePages.validation.idRequired");
		if (trimmed.length > 80) return translate("profilePages.validation.idTooLong");
		if (!/^[a-z0-9][a-z0-9._-]*$/.test(trimmed)) return translate("profilePages.validation.idFormat");
		return "";
	}

	function validateOptionalName(value: string): string {
		return value.trim().length > 120 ? translate("profilePages.validation.nameTooLong") : "";
	}

	function validateRequiredName(value: string): string {
		if (!value.trim()) return translate("profilePages.validation.nameRequired");
		return validateOptionalName(value);
	}

	function validateDescription(value: string): string {
		return value.trim().length > 1000 ? translate("profilePages.validation.descriptionTooLong") : "";
	}

	function optional(value: string): string | null {
		const trimmed = value.trim();
		return trimmed || null;
	}

	function formatError(value: unknown): string {
		return desktopErrorMessage(value, translate("errors.desktopUnavailable"));
	}
</script>

{#if route.kind === "list"}
	<div class="mx-auto w-full max-w-5xl">
		<ProfileList
			profiles={listItems}
			loading={loadingProfiles}
			error={profileError}
			busy={!!busyAction || useBuilding || useApplying}
			onNew={() => push("/codex/profiles/new")}
			onUse={openUse}
			onDetails={(profile) => push(`/codex/profiles/${encodeURIComponent(profile.id)}`)}
			onFork={(profile) => push(`/codex/profiles/${encodeURIComponent(profile.id)}/fork`)}
		/>
	</div>
{:else if route.kind === "new"}
	<div class="mx-auto flex w-full max-w-4xl flex-col gap-4">
		<Breadcrumb.Root>
			<Breadcrumb.List>
				<Breadcrumb.Item><Breadcrumb.Link href="#/codex/profiles">{$_("tabs.profiles")}</Breadcrumb.Link></Breadcrumb.Item>
				<Breadcrumb.Separator />
				<Breadcrumb.Item><Breadcrumb.Page>{$_("profilePages.new.title")}</Breadcrumb.Page></Breadcrumb.Item>
			</Breadcrumb.List>
		</Breadcrumb.Root>

		<div class="flex flex-col gap-1">
			<h2 class="text-xl font-semibold tracking-tight">{$_("profilePages.new.title")}</h2>
			<p class="text-sm text-muted-foreground">{$_("profilePages.new.description")}</p>
		</div>

		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.source.title")}</Card.Title>
				<Card.Description>{$_("profilePages.source.description")}</Card.Description>
				<Card.Action>
					<Button size="sm" variant="outline" disabled={sourceDetecting} onclick={() => detectSource()}>
						{#if sourceDetecting}<Spinner data-icon="inline-start" />{:else}<RefreshCwIcon data-icon="inline-start" />{/if}
						{$_("actions.refresh")}
					</Button>
				</Card.Action>
			</Card.Header>
			<Card.Content class="flex flex-col gap-3">
				<div class="grid gap-3 md:grid-cols-2">
					<div class="flex min-w-0 flex-col gap-1 rounded-lg border p-3">
						<div class="flex items-center justify-between gap-2">
							<span class="text-sm font-medium">config.toml</span>
							<Badge variant={sourceStatusVariant(detectResult?.config_status)}>{sourceStatusLabel(detectResult?.config_status, $locale)}</Badge>
						</div>
						<span class="truncate font-mono text-xs text-muted-foreground">{detectResult?.config_path || "—"}</span>
					</div>
					<div class="flex min-w-0 flex-col gap-1 rounded-lg border p-3">
						<div class="flex items-center justify-between gap-2">
							<span class="text-sm font-medium">auth.json</span>
							<Badge variant={sourceStatusVariant(detectResult?.auth_status)}>{sourceStatusLabel(detectResult?.auth_status, $locale)}</Badge>
						</div>
						<span class="truncate font-mono text-xs text-muted-foreground">{detectResult?.auth_path || "—"}</span>
					</div>
				</div>
				{#if !sourceReady}
					<Alert.Root variant="destructive">
						<AlertTriangleIcon data-icon="inline-start" />
						<Alert.Title>{$_("profilePages.source.notReadyTitle")}</Alert.Title>
						<Alert.Description>{$_("profilePages.source.notReadyDescription")}</Alert.Description>
					</Alert.Root>
				{:else}
					<Alert.Root>
						<CheckCircleIcon data-icon="inline-start" />
						<Alert.Title>{$_("profilePages.source.readyTitle")}</Alert.Title>
						<Alert.Description>{$_("profilePages.source.readyDescription")}</Alert.Description>
					</Alert.Root>
				{/if}
				{#if detectResult?.warnings?.length}
					<Alert.Root>
						<AlertTriangleIcon data-icon="inline-start" />
						<Alert.Title>{$_("profilePages.source.warningTitle")}</Alert.Title>
						<Alert.Description>{detectResult.warnings.join(" ")}</Alert.Description>
					</Alert.Root>
				{/if}
			</Card.Content>
		</Card.Root>

		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.form.profile")}</Card.Title>
				<Card.Description>{$_("profilePages.form.profileDescription")}</Card.Description>
			</Card.Header>
			<Card.Content>
				<ProfileForm
					bind:profileID
					bind:name={profileName}
					bind:description={profileDescription}
					idError={displayedIDError}
					nameError={displayedNameError}
					descriptionError={displayedDescriptionError}
				/>
			</Card.Content>
			<Card.Footer class="justify-end gap-2">
				<Button variant="outline" onclick={() => push("/codex/profiles")}>
					<ArrowLeftIcon data-icon="inline-start" />
					{$_("actions.cancel")}
				</Button>
				<Button disabled={!sourceReady || !formValid || !!busyAction} onclick={createProfile}>
					{#if busyAction === "profile-create"}<Spinner data-icon="inline-start" />{:else}<SaveIcon data-icon="inline-start" />{/if}
					{$_("actions.createProfile")}
				</Button>
			</Card.Footer>
		</Card.Root>
	</div>
{:else if detailLoading}
	<div class="mx-auto flex w-full max-w-5xl flex-col gap-4">
		<Skeleton class="h-5 w-48" />
		<Skeleton class="h-20 w-full" />
		<Skeleton class="h-52 w-full" />
		<Skeleton class="h-48 w-full" />
	</div>
{:else if detailError || !detail}
	<div class="mx-auto flex w-full max-w-3xl flex-col gap-4">
		<Alert.Root variant="destructive">
			<AlertTriangleIcon data-icon="inline-start" />
			<Alert.Title>{$_("profilePages.errorTitle")}</Alert.Title>
			<Alert.Description>{detailError || $_("errors.profileNotReady")}</Alert.Description>
		</Alert.Root>
		<Button class="self-start" variant="outline" onclick={() => push("/codex/profiles")}>
			<ArrowLeftIcon data-icon="inline-start" />
			{$_("actions.back")}
		</Button>
	</div>
{:else if route.kind === "detail"}
	<ProfileDetail
		{detail}
		{busyAction}
		updated={formatRelativeTime(detail.summary.updated_at_unix_ms, $locale)}
		onUse={() => openUse(profileListItem(detail!.summary))}
		onFork={() => push(`/codex/profiles/${encodeURIComponent(detail!.summary.profile.id)}/fork`)}
		onEdit={openEdit}
		onSync={openSync}
	/>
{:else}
	<div class="mx-auto flex w-full max-w-4xl flex-col gap-4">
		<Breadcrumb.Root>
			<Breadcrumb.List>
				<Breadcrumb.Item><Breadcrumb.Link href="#/codex/profiles">{$_("tabs.profiles")}</Breadcrumb.Link></Breadcrumb.Item>
				<Breadcrumb.Separator />
				<Breadcrumb.Item><Breadcrumb.Link href={`#/codex/profiles/${encodeURIComponent(detail.summary.profile.id)}`}>{detail.summary.profile.name || detail.summary.profile.id}</Breadcrumb.Link></Breadcrumb.Item>
				<Breadcrumb.Separator />
				<Breadcrumb.Item><Breadcrumb.Page>{$_("actions.fork")}</Breadcrumb.Page></Breadcrumb.Item>
			</Breadcrumb.List>
		</Breadcrumb.Root>

		<div class="flex flex-col gap-1">
			<h2 class="text-xl font-semibold tracking-tight">{$_("profilePages.fork.title", { values: { profile: detail.summary.profile.name || detail.summary.profile.id } })}</h2>
			<p class="text-sm text-muted-foreground">{$_("profilePages.fork.description")}</p>
		</div>

		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.fork.sourceTitle")}</Card.Title>
				<Card.Description>{$_("profilePages.fork.sourceDescription")}</Card.Description>
			</Card.Header>
			<Card.Content>
				<div class="flex flex-col text-sm">
					<div class="grid grid-cols-2 gap-6 pb-3">
						<div class="flex min-w-0 flex-col gap-1"><span class="text-xs text-muted-foreground">{$_("profilePages.form.profileID")}</span><span class="truncate font-mono">{detail.summary.profile.id}</span></div>
						<div class="flex min-w-0 flex-col gap-1"><span class="text-xs text-muted-foreground">{$_("profilePages.detail.name")}</span><span class="truncate">{detail.summary.profile.name || detail.summary.profile.id}</span></div>
					</div>
					<Separator />
					{#if detail.summary.profile.description}
						<div class="flex min-w-0 flex-col gap-1 py-3"><span class="text-xs text-muted-foreground">{$_("profilePages.form.description")}</span><span>{detail.summary.profile.description}</span></div>
						<Separator />
					{/if}
					<div class="grid grid-cols-2 gap-6 pt-3">
						<div class="flex flex-col gap-1"><span class="text-xs text-muted-foreground">{$_("profilePages.detail.model")}</span><span>{detail.summary.model || "—"}</span></div>
						<div class="flex flex-col gap-1"><span class="text-xs text-muted-foreground">{$_("profilePages.detail.account")}</span><span>{detail.summary.codex_account_id || $_("profile.noAccount")}</span></div>
					</div>
				</div>
			</Card.Content>
		</Card.Root>

		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.form.profile")}</Card.Title>
				<Card.Description>{$_("profilePages.fork.profileDescription")}</Card.Description>
			</Card.Header>
			<Card.Content>
				<ProfileForm
					bind:profileID
					bind:name={profileName}
					bind:description={profileDescription}
					idError={displayedIDError}
					nameError={displayedNameError}
					descriptionError={displayedDescriptionError}
				/>
			</Card.Content>
		</Card.Root>

		<Card.Root>
			<Card.Header>
				<Card.Title>{$_("profilePages.fork.authBinding")}</Card.Title>
				<Card.Description>{$_("profilePages.fork.authBindingDescription")}</Card.Description>
			</Card.Header>
			<Card.Content>
				<Field.FieldSet>
					<Field.FieldLegend class="sr-only">{$_("profilePages.fork.authBinding")}</Field.FieldLegend>
					<RadioGroup.Root bind:value={forkAuthBinding}>
						<Field.FieldLabel class="rounded-lg border p-3 has-[[data-checked]]:border-primary has-[[data-checked]]:bg-muted/50" for="fork-auth-share">
							<Field.Field orientation="horizontal">
								<Field.FieldContent>
									<Field.FieldTitle>{$_("profilePages.fork.shareParent")}</Field.FieldTitle>
									<Field.FieldDescription>{$_("profilePages.fork.shareParentDescription")}</Field.FieldDescription>
								</Field.FieldContent>
								<RadioGroup.Item id="fork-auth-share" value="share-parent" />
							</Field.Field>
						</Field.FieldLabel>
						<Field.FieldLabel class="rounded-lg border p-3 has-[[data-checked]]:border-primary has-[[data-checked]]:bg-muted/50" for="fork-auth-copy">
							<Field.Field orientation="horizontal">
								<Field.FieldContent>
									<Field.FieldTitle>{$_("profilePages.fork.copyNew")}</Field.FieldTitle>
									<Field.FieldDescription>{$_("profilePages.fork.copyNewDescription")}</Field.FieldDescription>
								</Field.FieldContent>
								<RadioGroup.Item id="fork-auth-copy" value="copy-new" />
							</Field.Field>
						</Field.FieldLabel>
					</RadioGroup.Root>
				</Field.FieldSet>
			</Card.Content>
			<Card.Footer class="justify-end gap-2">
				<Button variant="outline" onclick={() => push(`/codex/profiles/${encodeURIComponent(detail!.summary.profile.id)}`)}>
					<ArrowLeftIcon data-icon="inline-start" />
					{$_("actions.cancel")}
				</Button>
				<Button disabled={!formValid || !!busyAction} onclick={forkProfile}>
					{#if busyAction === "profile-fork"}<Spinner data-icon="inline-start" />{:else}<GitForkIcon data-icon="inline-start" />{/if}
					{$_("actions.createFork")}
				</Button>
			</Card.Footer>
		</Card.Root>
	</div>
{/if}

<Dialog.Root bind:open={editOpen}>
	<Dialog.Content class="sm:max-w-[520px]">
		<Dialog.Header>
			<Dialog.Title>{$_("profilePages.edit.title")}</Dialog.Title>
			<Dialog.Description>{$_("profilePages.edit.description")}</Dialog.Description>
		</Dialog.Header>
		<Field.FieldGroup>
			<Field.Field data-invalid={!!editNameError}>
				<Field.FieldLabel for="edit-profile-name">{$_("profilePages.form.name")}</Field.FieldLabel>
				<Input id="edit-profile-name" bind:value={editName} maxlength={120} aria-invalid={!!editNameError} />
				<Field.FieldError errors={editNameError ? [{ message: editNameError }] : []} />
			</Field.Field>
			<Field.FieldSeparator />
			<Field.Field data-invalid={!!editDescriptionError}>
				<Field.FieldLabel for="edit-profile-description">{$_("profilePages.form.description")}</Field.FieldLabel>
				<Textarea id="edit-profile-description" bind:value={editDescription} maxlength={1000} rows={4} aria-invalid={!!editDescriptionError} />
				<Field.FieldError errors={editDescriptionError ? [{ message: editDescriptionError }] : []} />
			</Field.Field>
		</Field.FieldGroup>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (editOpen = false)}>{$_("actions.cancel")}</Button>
			<Button disabled={busyAction === "profile-metadata"} onclick={saveMetadata}>
				{#if busyAction === "profile-metadata"}<Spinner data-icon="inline-start" />{/if}
				{$_("actions.saveChanges")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root open={syncOpen} onOpenChange={handleSyncOpenChange}>
	<Dialog.Content class="sm:max-w-[600px]">
		<Dialog.Header>
			<Dialog.Title>{syncStage === "confirm" ? $_("profilePages.sync.title") : $_("profilePages.sync.conflictTitle")}</Dialog.Title>
			<Dialog.Description>{syncStage === "confirm" ? $_("profilePages.sync.description") : $_("profilePages.sync.conflictDescription")}</Dialog.Description>
		</Dialog.Header>

		{#if syncStage === "confirm"}
			<div class="flex flex-col gap-3">
				{#if syncDetecting}
					<Skeleton class="h-16 w-full" />
				{:else}
					<div class="grid gap-3 md:grid-cols-2">
						<div class="flex min-w-0 flex-col gap-1 rounded-lg border p-3">
							<div class="flex items-center justify-between gap-2"><span class="text-sm font-medium">config.toml</span><Badge variant={sourceStatusVariant(detectResult?.config_status)}>{sourceStatusLabel(detectResult?.config_status, $locale)}</Badge></div>
							<span class="truncate font-mono text-xs text-muted-foreground">{detectResult?.config_path || "—"}</span>
						</div>
						<div class="flex min-w-0 flex-col gap-1 rounded-lg border p-3">
							<div class="flex items-center justify-between gap-2"><span class="text-sm font-medium">auth.json</span><Badge variant={sourceStatusVariant(detectResult?.auth_status)}>{sourceStatusLabel(detectResult?.auth_status, $locale)}</Badge></div>
							<span class="truncate font-mono text-xs text-muted-foreground">{detectResult?.auth_path || "—"}</span>
						</div>
					</div>
				{/if}
				<Alert.Root>
					<RefreshCwIcon data-icon="inline-start" />
					<Alert.Title>{$_("profilePages.sync.replaceTitle")}</Alert.Title>
					<Alert.Description>{$_("profilePages.sync.replaceDescription")}</Alert.Description>
				</Alert.Root>
			</div>
		{:else}
			<Field.FieldSet>
				<Field.FieldLegend>{$_("profilePages.sync.authChoice")}</Field.FieldLegend>
				<Field.FieldDescription>{$_("profilePages.sync.authChoiceDescription")}</Field.FieldDescription>
				<RadioGroup.Root bind:value={syncAuthUpdate}>
					<Field.FieldLabel class="rounded-lg border p-3 has-[[data-checked]]:border-primary has-[[data-checked]]:bg-muted/50" for="sync-update-shared">
						<Field.Field orientation="horizontal">
							<Field.FieldContent><Field.FieldTitle>{$_("profilePages.sync.updateShared")}</Field.FieldTitle><Field.FieldDescription>{$_("profilePages.sync.updateSharedDescription")}</Field.FieldDescription></Field.FieldContent>
							<RadioGroup.Item id="sync-update-shared" value="update-shared" />
						</Field.Field>
					</Field.FieldLabel>
					<Field.FieldLabel class="rounded-lg border p-3 has-[[data-checked]]:border-primary has-[[data-checked]]:bg-muted/50" for="sync-fork-new">
						<Field.Field orientation="horizontal">
							<Field.FieldContent><Field.FieldTitle>{$_("profilePages.sync.forkNew")}</Field.FieldTitle><Field.FieldDescription>{$_("profilePages.sync.forkNewDescription")}</Field.FieldDescription></Field.FieldContent>
							<RadioGroup.Item id="sync-fork-new" value="fork-new" />
						</Field.Field>
					</Field.FieldLabel>
				</RadioGroup.Root>
			</Field.FieldSet>
		{/if}

		{#if syncError}
			<Alert.Root variant="destructive">
				<AlertTriangleIcon data-icon="inline-start" />
				<Alert.Title>{$_("profilePages.sync.errorTitle")}</Alert.Title>
				<Alert.Description>{syncError}</Alert.Description>
			</Alert.Root>
		{/if}

		<Dialog.Footer>
			<Button variant="outline" onclick={() => handleSyncOpenChange(false)}>{$_("actions.cancel")}</Button>
			<Button disabled={syncDetecting || busyAction === "profile-sync" || (syncStage === "confirm" ? !sourceReady : !syncAuthUpdate)} onclick={syncProfile}>
				{#if busyAction === "profile-sync"}<Spinner data-icon="inline-start" />{/if}
				{syncStage === "confirm" ? $_("actions.updateProfile") : $_("actions.continue")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<UseProfileDialog
	bind:open={useOpen}
	profile={useProfile}
	currentProfile={activeProfileID}
	plan={usePlan}
	building={useBuilding}
	applying={useApplying}
	inlineError={useInlineError}
	onClose={closeUse}
	onConfirm={confirmUse}
/>
