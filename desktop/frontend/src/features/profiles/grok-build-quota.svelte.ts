import type { CancellablePromise } from "@wailsio/runtime";

import { GrokBuildService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
import type {
	GrokBuildProfileQuota,
	ProfileSummary,
} from "../../../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
import { GrokBuildProfileQuotaStatus } from "../../../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
import { isCancelError } from "$lib/desktop-errors";
import {
	grokBuildQuotaBindingKey,
	pruneGrokBuildQuotaRecords,
} from "./grok-build-quota-policy.js";

export type GrokBuildQuotaCheckOutcome = "updated" | "failed" | "never";

export type GrokBuildQuotaCheck = {
	profileID: string;
	credentialID: string;
	configSetID: string;
	checkedAtUnixMS: number;
	outcome: GrokBuildQuotaCheckOutcome;
};

type ControllerOptions = {
	showError: (value: unknown) => void;
};

type QuotaReadIntent = {
	profileID: string;
	bindingKey: string;
	credentialID: string;
	configSetID: string;
	requestID: number;
};

type InFlightRequest = QuotaReadIntent & {
	promise: CancellablePromise<GrokBuildProfileQuota>;
	completion: Promise<GrokBuildProfileQuota | null>;
	invalidated: boolean;
};

type PendingRequest = {
	intent: QuotaReadIntent;
	promise: Promise<GrokBuildProfileQuota | null>;
	resolve: (value: GrokBuildProfileQuota | null) => void;
};

export class GrokBuildQuotaController {
	quotaByBindingKey = $state<Record<string, GrokBuildProfileQuota>>({});
	checkByBindingKey = $state<Record<string, GrokBuildQuotaCheck>>({});
	loadingProfileID = $state("");

	#profiles = $state<ProfileSummary[]>([]);
	#activeProfileID = "";
	#inFlight: InFlightRequest | null = null;
	#pending: PendingRequest | null = null;
	#nextRequestID = 0;
	#options: ControllerOptions;

	constructor(options: ControllerOptions) {
		this.#options = options;
	}

	setProfiles(profiles: ProfileSummary[], activeProfileID?: string) {
		this.#profiles = profiles;
		this.#activeProfileID = activeProfileID === undefined
			? profiles.find((summary) => summary.active)?.profile.id ?? ""
			: activeProfileID;
		this.quotaByBindingKey = pruneGrokBuildQuotaRecords(profiles, this.quotaByBindingKey) as Record<string, GrokBuildProfileQuota>;
		this.checkByBindingKey = pruneGrokBuildQuotaRecords(profiles, this.checkByBindingKey) as Record<string, GrokBuildQuotaCheck>;
		if (this.#inFlight && !this.#inFlight.invalidated && !this.intentStillTargetsActiveProfile(this.#inFlight)) {
			this.#inFlight.invalidated = true;
			this.#inFlight.promise.cancel("profile-no-longer-active");
		}
		if (this.#pending && !this.intentStillTargetsActiveProfile(this.#pending.intent)) {
			const pending = this.#pending;
			this.#pending = null;
			pending.resolve(null);
		}
		this.syncLoadingProfileID();
	}

	quotaForSummary(summary: ProfileSummary): GrokBuildProfileQuota | null {
		const bindingKey = grokBuildQuotaBindingKey(summary);
		const quota = bindingKey ? this.quotaByBindingKey[bindingKey] : null;
		if (!quota) return null;
		return {
			...quota,
			profile_id: summary.profile.id,
			credential_id: summary.credential_id ?? "",
			config_set_id: summary.config_set_id ?? "",
			status: summary.active ? quota.status : GrokBuildProfileQuotaStatus.GrokBuildProfileQuotaInactive,
		};
	}

	checkForSummary(summary: ProfileSummary): GrokBuildQuotaCheck {
		const bindingKey = grokBuildQuotaBindingKey(summary);
		const check = bindingKey ? this.checkByBindingKey[bindingKey] : null;
		if (check) return { ...check, profileID: summary.profile.id };
		return {
			profileID: summary.profile.id,
			credentialID: summary.credential_id ?? "",
			configSetID: summary.config_set_id ?? "",
			checkedAtUnixMS: 0,
			outcome: "never",
		};
	}

	isLoading(profileID: string): boolean {
		return this.loadingProfileID === profileID;
	}

	readQuota(profileID: string): Promise<GrokBuildProfileQuota | null> {
		const summary = this.#profiles.find((value) => value.profile.id === profileID);
		const bindingKey = summary ? grokBuildQuotaBindingKey(summary) : "";
		if (!summary?.active || profileID !== this.#activeProfileID || !bindingKey) return Promise.resolve(null);
		return this.scheduleRead({
			profileID,
			bindingKey,
			credentialID: summary.credential_id ?? "",
			configSetID: summary.config_set_id ?? "",
			requestID: ++this.#nextRequestID,
		});
	}

	readCurrentQuota(profileID: string): Promise<GrokBuildProfileQuota | null> {
		if (!profileID || profileID !== this.#activeProfileID) return Promise.resolve(null);
		const summary = this.#profiles.find((value) => value.profile.id === profileID);
		const bindingKey = summary ? grokBuildQuotaBindingKey(summary) : "";
		return this.scheduleRead({
			profileID,
			bindingKey,
			credentialID: summary?.credential_id ?? "",
			configSetID: summary?.config_set_id ?? "",
			requestID: ++this.#nextRequestID,
		});
	}

	private scheduleRead(intent: QuotaReadIntent): Promise<GrokBuildProfileQuota | null> {
		if (this.#inFlight && !this.#inFlight.invalidated && this.sameIntent(this.#inFlight, intent)) {
			return this.#inFlight.completion;
		}
		if (this.#pending && this.sameIntent(this.#pending.intent, intent)) return this.#pending.promise;
		if (this.#inFlight) return this.queueRead(intent);
		return this.startRead(intent);
	}

	private startRead(intent: QuotaReadIntent): Promise<GrokBuildProfileQuota | null> {
		const promise = GrokBuildService.ReadProfileQuota(intent.profileID);
		const request: InFlightRequest = {
			...intent,
			promise,
			completion: Promise.resolve(null),
			invalidated: false,
		};
		this.#inFlight = request;
		request.completion = this.executeRead(request);
		this.syncLoadingProfileID();
		return request.completion;
	}

	private async executeRead(request: InFlightRequest): Promise<GrokBuildProfileQuota | null> {
		const previous = request.bindingKey ? this.quotaByBindingKey[request.bindingKey] ?? null : null;
		try {
			const result = await request.promise;
			if (!this.resultCanCommit(request, result)) return result;
			const bindingKey = grokBuildQuotaBindingKey(result);
			const merged = this.preserveSnapshot(result, this.quotaByBindingKey[bindingKey] ?? previous);
			this.quotaByBindingKey = { ...this.quotaByBindingKey, [bindingKey]: merged };
			this.checkByBindingKey = {
				...this.checkByBindingKey,
				[bindingKey]: {
					profileID: request.profileID,
					credentialID: result.credential_id,
					configSetID: result.config_set_id,
					checkedAtUnixMS: result.status === "available"
						? result.snapshot?.fetched_at_unix_ms || Date.now()
						: Date.now(),
					outcome: result.status === "available" ? "updated" : "failed",
				},
			};
			return result;
		} catch (error) {
			if (!isCancelError(error) && this.requestCanCommit(request)) {
				if (request.bindingKey) {
					const unavailable: GrokBuildProfileQuota = {
						profile_id: request.profileID,
						credential_id: request.credentialID,
						config_set_id: request.configSetID,
						status: GrokBuildProfileQuotaStatus.GrokBuildProfileQuotaUnavailable,
					};
					this.quotaByBindingKey = {
						...this.quotaByBindingKey,
						[request.bindingKey]: this.preserveSnapshot(unavailable, previous),
					};
					this.checkByBindingKey = {
						...this.checkByBindingKey,
						[request.bindingKey]: {
							profileID: request.profileID,
							credentialID: request.credentialID,
							configSetID: request.configSetID,
							checkedAtUnixMS: Date.now(), outcome: "failed",
						},
					};
				}
				this.#options.showError(error);
			}
			return null;
		} finally {
			if (this.#inFlight === request) {
				this.#inFlight = null;
				this.drainPending();
			}
		}
	}

	private queueRead(intent: QuotaReadIntent): Promise<GrokBuildProfileQuota | null> {
		if (this.#pending) this.#pending.resolve(null);
		let resolve!: (value: GrokBuildProfileQuota | null) => void;
		const promise = new Promise<GrokBuildProfileQuota | null>((done) => { resolve = done; });
		this.#pending = { intent, promise, resolve };
		if (this.#inFlight && !this.#inFlight.invalidated && !this.intentStillTargetsActiveProfile(this.#inFlight)) {
			this.#inFlight.invalidated = true;
			this.#inFlight.promise.cancel("profile-no-longer-active");
		}
		this.syncLoadingProfileID();
		return promise;
	}

	private drainPending() {
		const pending = this.#pending;
		this.#pending = null;
		if (!pending) {
			this.syncLoadingProfileID();
			return;
		}
		if (!this.intentStillTargetsActiveProfile(pending.intent)) {
			pending.resolve(null);
			this.syncLoadingProfileID();
			return;
		}
		this.startRead(pending.intent).then(pending.resolve);
	}

	reset(reason = "reset") {
		if (this.#inFlight && !this.#inFlight.invalidated) {
			this.#inFlight.invalidated = true;
			this.#inFlight.promise.cancel(reason);
		}
		this.#pending?.resolve(null);
		this.#pending = null;
		this.#profiles = [];
		this.#activeProfileID = "";
		this.quotaByBindingKey = {};
		this.checkByBindingKey = {};
		this.loadingProfileID = "";
	}

	stop() {
		this.reset("unmount");
	}

	private sameIntent(left: QuotaReadIntent, right: QuotaReadIntent): boolean {
		return left.profileID === right.profileID && left.bindingKey === right.bindingKey;
	}

	private intentStillTargetsActiveProfile(intent: QuotaReadIntent): boolean {
		if (this.#activeProfileID !== intent.profileID) return false;
		const current = this.#profiles.find((summary) => summary.profile.id === intent.profileID);
		return !intent.bindingKey || !current || grokBuildQuotaBindingKey(current) === intent.bindingKey;
	}

	private requestCanCommit(request: InFlightRequest): boolean {
		return this.#inFlight?.requestID === request.requestID
			&& !request.invalidated
			&& this.intentStillTargetsActiveProfile(request);
	}

	private resultCanCommit(request: InFlightRequest, result: GrokBuildProfileQuota): boolean {
		if (!this.requestCanCommit(request)
			|| result.status === GrokBuildProfileQuotaStatus.GrokBuildProfileQuotaInactive
			|| result.profile_id !== request.profileID) return false;
		const bindingKey = grokBuildQuotaBindingKey(result);
		if (!bindingKey || (request.bindingKey && bindingKey !== request.bindingKey)) return false;
		const current = this.#profiles.find((summary) => summary.profile.id === request.profileID);
		return !current || grokBuildQuotaBindingKey(current) === bindingKey;
	}

	private syncLoadingProfileID() {
		this.loadingProfileID = this.#pending?.intent.profileID
			?? (this.#inFlight?.invalidated ? "" : this.#inFlight?.profileID)
			?? "";
	}

	private preserveSnapshot(result: GrokBuildProfileQuota, previous: GrokBuildProfileQuota | null): GrokBuildProfileQuota {
		if (result.snapshot || !previous?.snapshot) return result;
		return { ...result, snapshot: previous.snapshot };
	}
}
