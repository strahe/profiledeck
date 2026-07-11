import { Events, type CancellablePromise } from "@wailsio/runtime";
import { getContext, setContext } from "svelte";

import { CodexService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
import type {
	CodexProfileQuotaRuntimeStatus,
	CodexQuotaRuntimeStatus,
} from "../../../bindings/github.com/strahe/profiledeck/desktop/backend/models";
import type {
	CodexProfileQuota,
	CodexProfileSettings,
	CodexProfileSummary,
	CodexSettings,
} from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
import { isCancelError } from "$lib/desktop-errors";
import { translate } from "$lib/i18n";

export const quotaIntervals = [0, 300, 600, 1800, 3600] as const;
export const usageIntervals = [5, 15, 30, 60] as const;

type RuntimeOptions = {
	showError: (value: unknown) => void;
	showNotice: (title: string, description: string) => void;
};

const runtimeContextKey = Symbol("profiledeck-codex-runtime");

export class CodexRuntimeController {
	settings = $state<CodexSettings | null>(null);
	runtime = $state<CodexQuotaRuntimeStatus>({ app_server_status: "unknown", profiles: [] });
	quotaByProfileID = $state<Record<string, CodexProfileQuota>>({});
	loading = $state(true);
	busyKeys = $state<string[]>([]);
	requestedQuotaProfileIDs = $state<string[]>([]);
	runtimeQuotaLoadingProfileIDs = $state<string[]>([]);

	#profiles = $state<CodexProfileSummary[]>([]);
	#profileSignature = "";
	#inFlight = new Map<string, CancellablePromise<unknown>>();
	#disposeEvent: (() => void) | null = null;
	#started = false;
	#loadSequence = 0;
	#options: RuntimeOptions;

	constructor(options: RuntimeOptions) {
		this.#options = options;
	}

	start() {
		if (this.#started) return () => this.stop();
		this.#started = true;
		void this.load();
		this.#disposeEvent = Events.On("profiledeck:codex-quota-status", (event) => {
			const previousCompletion = this.latestRuntimeCompletion(this.runtime);
			const next = (event.data as CodexQuotaRuntimeStatus) ?? { app_server_status: "unknown", profiles: [] };
			this.applyRuntimeStatus(next);
			if (this.latestRuntimeCompletion(next) > previousCompletion) void this.refreshSettings(false);
		});
		return () => this.stop();
	}

	stop() {
		this.#disposeEvent?.();
		this.#disposeEvent = null;
		this.#started = false;
		for (const promise of this.#inFlight.values()) promise.cancel("unmount");
		this.#inFlight.clear();
	}

	setProfiles(profiles: CodexProfileSummary[]) {
		this.#profiles = profiles;
		this.applyRuntimeStatus(this.runtime);
		const signature = profiles
			.map((profile) => `${profile.profile.id}:${profile.credential_id ?? ""}:${profile.updated_at_unix_ms}`)
			.join("|");
		if (signature === this.#profileSignature) return;
		this.#profileSignature = signature;
		if (this.#started && !this.loading) void this.refreshSettings(false);
	}

	async load() {
		const sequence = ++this.#loadSequence;
		this.loading = true;
		try {
			const [settings, runtime] = await Promise.all([
				this.track("codex-settings", CodexService.GetSettings()),
				this.track("codex-quota-runtime", CodexService.QuotaRuntimeStatus()),
			]);
			this.settings = settings;
			this.applyRuntimeStatus(runtime);
		} catch (error) {
			if (!isCancelError(error)) this.#options.showError(error);
		} finally {
			if (sequence === this.#loadSequence) this.loading = false;
		}
	}

	async refreshSettings(showLoading = false) {
		const sequence = ++this.#loadSequence;
		if (showLoading) this.loading = true;
		try {
			this.settings = await this.track("codex-settings", CodexService.GetSettings());
		} catch (error) {
			if (!isCancelError(error)) this.#options.showError(error);
		} finally {
			if (sequence === this.#loadSequence) this.loading = false;
		}
	}

	settingsProfile(profileID: string): CodexProfileSettings | undefined {
		return this.settings?.profiles?.find((profile) => profile.profile_id === profileID);
	}

	runtimeProfile(profileID: string): CodexProfileQuotaRuntimeStatus | undefined {
		return this.runtime.profiles?.find((profile) => profile.profile_id === profileID);
	}

	quotaForSummary(summary: CodexProfileSummary): CodexProfileQuota | null {
		const quota = this.quotaByProfileID[summary.profile.id];
		if (!quota) return null;
		return (quota.credential_id ?? "") === (summary.credential_id ?? "") ? quota : null;
	}

	isQuotaLoading(profileID: string): boolean {
		return this.requestedQuotaProfileIDs.includes(profileID) || this.runtimeQuotaLoadingProfileIDs.includes(profileID);
	}

	isBusy(key: string): boolean {
		return this.busyKeys.includes(key);
	}

	async readQuota(profileID: string) {
		if (!profileID || this.isQuotaLoading(profileID)) return;
		this.requestedQuotaProfileIDs = [...this.requestedQuotaProfileIDs, profileID];
		try {
			const quota = await this.track(`profile-quota:${profileID}`, CodexService.ReadProfileQuota(profileID));
			this.quotaByProfileID = { ...this.quotaByProfileID, [quota.profile_id]: quota };
		} catch (error) {
			if (!isCancelError(error)) this.#options.showError(error);
		} finally {
			this.requestedQuotaProfileIDs = this.requestedQuotaProfileIDs.filter((value) => value !== profileID);
		}
	}

	async changeUsageSyncInterval(value: string) {
		const interval = Number(value);
		if (!usageIntervals.includes(interval as (typeof usageIntervals)[number]) || this.settings?.usage_sync_interval_seconds === interval) return;
		await this.update("usage", { config_dir: "", usage_sync_interval_seconds: interval });
	}

	async changeQuotaInterval(profile: CodexProfileSettings, value: string) {
		const interval = Number(value);
		if (!quotaIntervals.includes(interval as (typeof quotaIntervals)[number]) || profile.quota_refresh_interval_seconds === interval) return;
		if (!profile.quota_supported && interval !== 0) return;
		await this.update(`quota:${profile.profile_id}`, {
			config_dir: "",
			profile_id: profile.profile_id,
			quota_refresh_interval_seconds: interval,
		});
	}

	async changeKeepalive(profile: CodexProfileSettings, enabled: boolean) {
		if (profile.auth_keepalive_enabled === enabled || (!profile.auth_keepalive_supported && enabled)) return;
		await this.update(`keepalive:${profile.profile_id}`, {
			config_dir: "",
			profile_id: profile.profile_id,
			auth_keepalive_enabled: enabled,
		});
	}

	applyRuntimeStatus(status: CodexQuotaRuntimeStatus | null | undefined) {
		if (!status) return;
		this.runtime = status;
		const next = { ...this.quotaByProfileID };
		const runtimeProfileIDs = new Set((status.profiles ?? []).map((value) => value.profile_id));
		for (const profileID of Object.keys(next)) {
			if (!runtimeProfileIDs.has(profileID)) delete next[profileID];
		}
		for (const profileStatus of status.profiles ?? []) {
			if (!profileStatus.last_task && !profileStatus.last_completed_at_unix_ms && !profileStatus.snapshot) {
				delete next[profileStatus.profile_id];
				continue;
			}
			// Keepalive changes auth freshness without reading rate limits, so it must not replace a quota result.
			if (profileStatus.last_task !== "quota" || (!profileStatus.last_completed_at_unix_ms && !profileStatus.snapshot)) continue;
			const summary = this.#profiles.find((profile) => profile.profile.id === profileStatus.profile_id);
			next[profileStatus.profile_id] = {
				profile_id: profileStatus.profile_id,
				credential_id: summary?.credential_id,
				status: profileStatus.status,
				snapshot: profileStatus.snapshot,
			};
		}
		this.quotaByProfileID = next;
		this.runtimeQuotaLoadingProfileIDs = (status.profiles ?? [])
			.filter((value) => value.running && value.last_task === "quota")
			.map((value) => value.profile_id);
	}

	async update(key: string, request: Parameters<typeof CodexService.UpdateSettings>[0]) {
		if (this.isBusy(key)) return;
		this.busyKeys = [...this.busyKeys, key];
		try {
			this.settings = await this.track(`codex-settings:${key}`, CodexService.UpdateSettings(request));
			this.applyRuntimeStatus(await this.track("codex-quota-runtime", CodexService.QuotaRuntimeStatus()));
			this.#options.showNotice(translate("notice.settingsSaved.title"), translate("notice.settingsSaved.codexDescription"));
		} catch (error) {
			if (!isCancelError(error)) {
				this.#options.showError(error);
				await this.load();
			}
		} finally {
			this.busyKeys = this.busyKeys.filter((value) => value !== key);
		}
	}

	track<T>(key: string, promise: CancellablePromise<T>): CancellablePromise<T> {
		this.#inFlight.get(key)?.cancel("replaced");
		this.#inFlight.set(key, promise as CancellablePromise<unknown>);
		promise.finally(() => {
			if (this.#inFlight.get(key) === promise) this.#inFlight.delete(key);
		}).catch(() => {});
		return promise;
	}

	latestRuntimeCompletion(value: CodexQuotaRuntimeStatus): number {
		return Math.max(0, ...(value.profiles ?? []).map((profile) => profile.last_completed_at_unix_ms || 0));
	}
}

export function provideCodexRuntime(options: RuntimeOptions): CodexRuntimeController {
	return setContext(runtimeContextKey, new CodexRuntimeController(options));
}

export function useCodexRuntime(): CodexRuntimeController {
	const runtime = getContext<CodexRuntimeController | undefined>(runtimeContextKey);
	if (!runtime) throw new Error("Codex runtime controller is not available");
	return runtime;
}
