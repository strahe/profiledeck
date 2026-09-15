import { act, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { UsageAutoSyncStatus } from "../bindings/github.com/strahe/profiledeck/desktop/backend/models";
import {
	UsageRangePreset,
	type UsageReportResult,
} from "../bindings/github.com/strahe/profiledeck/internal/usage/models";

const backend = vi.hoisted(() => ({
	report: vi.fn(),
	syncNow: vi.fn(),
}));
const runtime = vi.hoisted(() => ({
	handler: null as ((event: { data: unknown }) => void) | null,
	on: vi.fn(),
}));

vi.mock("../bindings/github.com/strahe/profiledeck/desktop/backend", () => ({
	UsageService: {
		Report: backend.report,
		SyncNow: backend.syncNow,
	},
}));
vi.mock("@wailsio/runtime", () => ({
	Events: { On: runtime.on },
}));

import UsagePage from "../src/features/usage/UsagePage.svelte";
import TestProviders from "./TestProviders.svelte";

function cancellable<T>(promise: Promise<T>) {
	return Object.assign(promise, { cancel: vi.fn() });
}

function syncStatus(overrides: Partial<UsageAutoSyncStatus> = {}): UsageAutoSyncStatus {
	return {
		provider_id: "grok-build",
		revision: 1,
		interval_seconds: 15,
		syncing: true,
		outcome: "syncing",
		last_started_at_unix_ms: 100,
		last_completed_at_unix_ms: 0,
		last_success_at_unix_ms: 0,
		import_error_count: 0,
		...overrides,
	};
}

function usageReport(): UsageReportResult {
	return {
		provider_id: "grok-build",
		source: "grok-build-session-jsonl",
		sources: ["grok-build-session-jsonl"],
		range: {
			preset: UsageRangePreset.UsageRange7Days,
			start_unix_ms: 1,
			end_exclusive_unix_ms: 2,
			bucket_unit: "day",
			time_zone: "UTC",
		},
		summary: {
			event_count: 1,
			session_count: 1,
			fresh_input_tokens: 1_000_000,
			input_tokens: 1_000_000,
			cached_input_tokens: 0,
			output_tokens: 1_000_000,
			total_tokens: 2_000_000,
			cache_hit_rate: 0,
			known_estimated_cost_usd: "8.000000",
			cost_status: "estimated",
			estimated_cost_event_count: 1,
			partial_cost_event_count: 0,
			unknown_cost_event_count: 0,
			estimated_token_count: 2_000_000,
			pricing_coverage: 1,
			known_reported_cost_usd: "0.5452000000",
			reported_cost_status: "reported",
			reported_cost_event_count: 1,
			partial_reported_cost_event_count: 0,
			unknown_reported_cost_event_count: 0,
			reported_cost_token_count: 2_000_000,
			reported_cost_coverage: 1,
			undated_event_count: 0,
		},
		trend: [],
		models: [],
		import: {
			tracked_files: 1,
			last_synced_at_unix_ms: 200,
			invalid_lines: 0,
			unsupported_lines: 0,
		},
		pricing: {
			basis: "xai-standard-api-short-context",
			source_url: "https://docs.x.ai/developers/models/grok-4.5",
			verified_at: "2026-07-30",
			historical_repricing: false,
		},
	};
}

function emptyUsageReport(): UsageReportResult {
	const report = usageReport();
	return {
		...report,
		summary: {
			...report.summary,
			event_count: 0,
			session_count: 0,
			fresh_input_tokens: 0,
			input_tokens: 0,
			output_tokens: 0,
			total_tokens: 0,
			known_estimated_cost_usd: "0.000000",
			estimated_cost_event_count: 0,
			estimated_token_count: 0,
			known_reported_cost_usd: "0.0000000000",
			reported_cost_status: "unknown",
			reported_cost_event_count: 0,
			unknown_reported_cost_event_count: 0,
			reported_cost_token_count: 0,
			reported_cost_coverage: 0,
		},
	};
}

describe("UsagePage initial sync", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		runtime.handler = null;
	});

	it("loads stored data immediately and suppresses an empty result until sync finishes", async () => {
		let resolveSync!: (status: UsageAutoSyncStatus) => void;
		runtime.handler = null;
		runtime.on.mockImplementation((_name: string, handler: (event: { data: unknown }) => void) => {
			runtime.handler = handler;
			return vi.fn();
		});
		backend.syncNow.mockReturnValue(cancellable(new Promise<UsageAutoSyncStatus>((resolve) => {
			resolveSync = resolve;
		})));
		backend.report
			.mockReturnValueOnce(cancellable(Promise.resolve(emptyUsageReport())))
			.mockReturnValue(cancellable(Promise.resolve(usageReport())));

		render(UsagePage, {
			providerID: "grok-build",
			providerName: "Grok Build",
			providerExists: true,
			onOpenProfiles: vi.fn(),
			showError: vi.fn(),
		}, { wrapper: TestProviders });

		await waitFor(() => expect(backend.syncNow).toHaveBeenCalledWith("grok-build"));
		await waitFor(() => expect(backend.report).toHaveBeenCalledWith("grok-build", "7d"));
		expect(screen.getByText("Syncing usage…")).toBeInTheDocument();
		expect(screen.queryByText("No usage yet")).not.toBeInTheDocument();

		await act(() => runtime.handler?.({ data: syncStatus({
			provider_id: "codex",
			revision: 99,
			syncing: false,
			outcome: "error",
			error: { code: "TIMEOUT", message: "other Provider failed" },
		}) }));
		expect(backend.report).toHaveBeenCalledTimes(1);
		expect(screen.getByText("Syncing usage…")).toBeInTheDocument();
		expect(screen.queryByText("ProfileDeck will retry on the next interval.")).not.toBeInTheDocument();

		const syncing = syncStatus({
			error: { code: "TIMEOUT", message: "stale error" },
		});
		await act(() => runtime.handler?.({ data: syncing }));
		expect(screen.getByText("Syncing usage…")).toBeInTheDocument();
		expect(screen.queryByText("ProfileDeck will retry on the next interval.")).not.toBeInTheDocument();

		const completed = syncStatus({
			revision: 2,
			syncing: false,
			outcome: "success",
			last_completed_at_unix_ms: 200,
			last_success_at_unix_ms: 200,
			error: null,
		});
		await act(() => {
			runtime.handler?.({ data: completed });
			resolveSync(completed);
		});

		await waitFor(() => expect(backend.report).toHaveBeenCalledTimes(2));
		expect(await screen.findByText("$8.00")).toBeInTheDocument();
		expect(screen.getByText("$0.5452")).toBeInTheDocument();
		expect(screen.getByText("Grok-reported cost")).toBeInTheDocument();
		expect(screen.getByRole("radio", { name: "Grok reported" })).toBeInTheDocument();
		expect(screen.getByRole("columnheader", { name: "Grok reported" })).toBeInTheDocument();
		expect(screen.queryByText("No usage yet")).not.toBeInTheDocument();
	});

	it("shows a partial reported subtotal without claiming token coverage", async () => {
		const partialReport = usageReport();
		partialReport.summary = {
			...partialReport.summary,
			reported_cost_status: "partial",
			reported_cost_event_count: 0,
			partial_reported_cost_event_count: 1,
			reported_cost_token_count: 0,
			reported_cost_coverage: 0,
		};
		runtime.on.mockReturnValue(vi.fn());
		backend.report.mockReturnValue(cancellable(Promise.resolve(partialReport)));
		backend.syncNow.mockReturnValue(cancellable(Promise.resolve(syncStatus({
			revision: 2,
			syncing: false,
			outcome: "success",
			last_completed_at_unix_ms: 200,
			last_success_at_unix_ms: 200,
		}))));

		render(UsagePage, {
			providerID: "grok-build",
			providerName: "Grok Build",
			providerExists: true,
			onOpenProfiles: vi.fn(),
			showError: vi.fn(),
		}, { wrapper: TestProviders });

		expect(await screen.findByText("$0.5452")).toBeInTheDocument();
		expect(screen.getByText("Known subtotal · some calls have no reported cost")).toBeInTheDocument();
		expect(screen.getByText("Partial")).toBeInTheDocument();
		expect(screen.queryByText(/complete cost for/)).not.toBeInTheDocument();
	});

});
