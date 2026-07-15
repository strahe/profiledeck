import type { CancellablePromise } from "@wailsio/runtime";

import { AntigravityService } from "../../../bindings/github.com/strahe/profiledeck/desktop/backend";
import type {
	AntigravityProfileQuota,
	AntigravityProfileSummary,
} from "../../../bindings/github.com/strahe/profiledeck/internal/antigravity/models";
import { AntigravityProfileQuotaStatus } from "../../../bindings/github.com/strahe/profiledeck/internal/antigravity/models";
import { isCancelError } from "$lib/desktop-errors";
import {
	AntigravityQuotaRequestGate,
	pruneAntigravityQuotaChecks,
	pruneAntigravityQuotaRecords,
} from "./antigravity-quota-policy.js";

export type AntigravityQuotaCheckOutcome = "updated" | "failed" | "never";

export type AntigravityQuotaCheck = {
	profileID: string;
	credentialID: string;
	checkedAtUnixMS: number;
	outcome: AntigravityQuotaCheckOutcome;
};

type ControllerOptions = {
	showError: (value: unknown) => void;
};

export class AntigravityQuotaController {
	quotaByProfileID = $state<Record<string, AntigravityProfileQuota>>({});
	checkByProfileID = $state<Record<string, AntigravityQuotaCheck>>({});
	loadingProfileIDs = $state<string[]>([]);

	#profiles = $state<AntigravityProfileSummary[]>([]);
	#inFlight = new Map<string, CancellablePromise<AntigravityProfileQuota>>();
	#requestedCredentialIDs = new Map<string, string>();
	#requestGate = new AntigravityQuotaRequestGate();
	#options: ControllerOptions;

	constructor(options: ControllerOptions) {
		this.#options = options;
	}

	setProfiles(profiles: AntigravityProfileSummary[]) {
		this.#profiles = profiles;
		const credentials = new Map(profiles.map((summary) => [summary.profile.id, summary.credential_id ?? ""]));
		this.quotaByProfileID = pruneAntigravityQuotaRecords(profiles, this.quotaByProfileID) as Record<string, AntigravityProfileQuota>;
		this.checkByProfileID = pruneAntigravityQuotaChecks(profiles, this.checkByProfileID) as Record<string, AntigravityQuotaCheck>;
		for (const [profileID, promise] of this.#inFlight) {
			if (credentials.get(profileID) === this.#requestedCredentialIDs.get(profileID)) continue;
			promise.cancel("profile-binding-changed");
			this.#inFlight.delete(profileID);
			this.#requestedCredentialIDs.delete(profileID);
			this.#requestGate.finish(profileID);
			this.loadingProfileIDs = this.loadingProfileIDs.filter((value) => value !== profileID);
		}
	}

	quotaForSummary(summary: AntigravityProfileSummary): AntigravityProfileQuota | null {
		const quota = this.quotaByProfileID[summary.profile.id];
		if (!quota) return null;
		return (quota.credential_id ?? "") === (summary.credential_id ?? "") ? quota : null;
	}

	checkForSummary(summary: AntigravityProfileSummary): AntigravityQuotaCheck {
		const check = this.checkByProfileID[summary.profile.id];
		if (check && check.credentialID === (summary.credential_id ?? "")) return check;
		return {
			profileID: summary.profile.id,
			credentialID: summary.credential_id ?? "",
			checkedAtUnixMS: 0,
			outcome: "never",
		};
	}

	isLoading(profileID: string): boolean {
		return this.loadingProfileIDs.includes(profileID);
	}

	async readQuota(profileID: string): Promise<AntigravityProfileQuota | null> {
		const summary = this.#profiles.find((value) => value.profile.id === profileID);
		if (!summary || !this.#requestGate.start(profileID)) return null;
		const credentialID = summary.credential_id ?? "";
		const previous = this.quotaForSummary(summary);
		this.loadingProfileIDs = [...this.loadingProfileIDs, profileID];
		this.#requestedCredentialIDs.set(profileID, credentialID);
		const promise = AntigravityService.ReadProfileQuota(profileID);
		this.#inFlight.set(profileID, promise);
		try {
			const result = await promise;
			if (!this.requestStillMatches(profileID, result.credential_id ?? "")) return result;
			const merged = this.preserveSnapshot(result, previous);
			this.quotaByProfileID = { ...this.quotaByProfileID, [profileID]: merged };
			this.checkByProfileID = {
				...this.checkByProfileID,
				[profileID]: {
					profileID,
					credentialID,
					checkedAtUnixMS: result.status === "available"
						? result.snapshot?.fetched_at_unix_ms || Date.now()
						: Date.now(),
					outcome: result.status === "available" ? "updated" : "failed",
				},
			};
			return result;
		} catch (error) {
			if (!isCancelError(error) && this.requestStillMatches(profileID, credentialID)) {
				const unavailable: AntigravityProfileQuota = {
					profile_id: profileID,
					credential_id: credentialID,
					status: AntigravityProfileQuotaStatus.AntigravityProfileQuotaUnavailable,
				};
				this.quotaByProfileID = {
					...this.quotaByProfileID,
					[profileID]: this.preserveSnapshot(unavailable, previous),
				};
				this.checkByProfileID = {
					...this.checkByProfileID,
					[profileID]: { profileID, credentialID, checkedAtUnixMS: Date.now(), outcome: "failed" },
				};
				this.#options.showError(error);
			}
			return null;
		} finally {
			if (this.#inFlight.get(profileID) === promise) {
				this.#inFlight.delete(profileID);
				this.#requestedCredentialIDs.delete(profileID);
				this.#requestGate.finish(profileID);
				this.loadingProfileIDs = this.loadingProfileIDs.filter((value) => value !== profileID);
			}
		}
	}

	reset(reason = "reset") {
		for (const promise of this.#inFlight.values()) promise.cancel(reason);
		this.#inFlight.clear();
		this.#requestedCredentialIDs.clear();
		this.#requestGate.clear();
		this.#profiles = [];
		this.quotaByProfileID = {};
		this.checkByProfileID = {};
		this.loadingProfileIDs = [];
	}

	stop() {
		this.reset("unmount");
	}

	requestStillMatches(profileID: string, credentialID: string): boolean {
		const current = this.#profiles.find((summary) => summary.profile.id === profileID);
		return !!current && (current.credential_id ?? "") === credentialID;
	}

	preserveSnapshot(result: AntigravityProfileQuota, previous: AntigravityProfileQuota | null): AntigravityProfileQuota {
		if (result.snapshot || !previous?.snapshot || (result.credential_id ?? "") !== (previous.credential_id ?? "")) return result;
		return { ...result, snapshot: previous.snapshot };
	}
}
