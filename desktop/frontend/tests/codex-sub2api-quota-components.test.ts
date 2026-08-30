import { render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import { locale } from "svelte-i18n";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CodexProfileQuota } from "../bindings/github.com/strahe/profiledeck/internal/codex/models";
import ProfileQuotaCard from "../src/features/profiles/ProfileQuotaCard.svelte";
import ProfileQuotaSummary from "../src/features/profiles/ProfileQuotaSummary.svelte";
import TestProviders from "./TestProviders.svelte";

function quota(overrides: Partial<CodexProfileQuota> = {}): CodexProfileQuota {
	return {
		profile_id: "work",
		credential_id: "credential",
		config_set_id: "config",
		source: "sub2api",
		status: "available",
		sub2api_snapshot: {
			fetched_at_unix_ms: Date.UTC(2026, 7, 2, 1, 0, 0),
			mode: "quota_limited",
			plan_name: "Team",
			key_state: "active",
			unit: "USD",
			unlimited: false,
			limit: 100,
			used: 25,
			remaining: 75,
			expires_at_unix_seconds: Date.UTC(2026, 8, 1, 0, 0, 0) / 1000,
			windows: [
				{ id: "5h", limit: 40, used: 10, remaining: 30, remaining_percent: 75 },
				{ id: "daily", limit: 100, used: 25, remaining: 75, remaining_percent: 75 },
			],
		},
		...overrides,
	};
}

function renderCard(value: CodexProfileQuota | null, loading = false) {
	return render(ProfileQuotaCard, {
		quota: value,
		loading,
		disabled: false,
		onRefresh: vi.fn(),
	}, { wrapper: TestProviders });
}

describe("Codex API service quota components", () => {
	beforeEach(async () => {
		locale.set("en");
		await tick();
	});

	it("shows remaining quota, plan, expiry, windows, and an HTTP warning", () => {
		renderCard(quota({ insecure_transport: true }));

		expect(screen.getByText("Team")).toBeInTheDocument();
		expect(screen.getByText("Active")).toBeInTheDocument();
		expect(screen.getByText("$75.00")).toBeInTheDocument();
		expect(screen.getByText("5 hours")).toBeInTheDocument();
		expect(screen.getByText("Daily")).toBeInTheDocument();
		expect(screen.getByText("API Key sent over an unencrypted connection")).toBeInTheDocument();
		expect(screen.getByText(/not protected by transport encryption/)).toBeInTheDocument();
	});

	it("shows unlimited subscriptions, negative wallet balances, and values without units", async () => {
		const view = renderCard(quota({
			sub2api_snapshot: {
				fetched_at_unix_ms: 1,
				mode: "unrestricted",
				plan_name: "Unlimited",
				key_state: "active",
				unlimited: true,
				remaining: null,
				windows: [],
			},
		}));
		expect(screen.getAllByText("Unlimited").length).toBeGreaterThan(0);

		await view.rerender({
			quota: quota({
				sub2api_snapshot: {
					fetched_at_unix_ms: 1,
					mode: "unrestricted",
					key_state: "active",
					unlimited: false,
					remaining: -12.5,
					balance: -12.5,
					windows: [],
				},
			}),
			loading: false,
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getAllByText("-12.5").length).toBeGreaterThan(0);
		expect(screen.queryByText(/USD/)).not.toBeInTheDocument();
	});

	it("keeps unknown three-letter units as plain text", () => {
		renderCard(quota({
			sub2api_snapshot: { ...quota().sub2api_snapshot!, unit: "TOK", remaining: 12.5 },
		}));
		expect(screen.getByText("12.5 TOK")).toBeInTheDocument();
	});

	it("keeps expired and exhausted keys visibly unhealthy", async () => {
		const view = renderCard(quota({
			sub2api_snapshot: { ...quota().sub2api_snapshot!, key_state: "expired" },
		}));
		expect(screen.getByText("Expired")).toHaveClass("text-destructive");

		await view.rerender({
			quota: quota({ sub2api_snapshot: { ...quota().sub2api_snapshot!, key_state: "quota_exhausted", remaining: 0 } }),
			loading: false,
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("Quota exhausted")).toHaveClass("text-destructive");
	});

	it("keeps a successful result visible while loading and hides stale values after failure", async () => {
		const view = renderCard(quota(), true);
		expect(screen.getByText("$75.00")).toBeInTheDocument();

		await view.rerender({
			quota: quota({ status: "unavailable", insecure_transport: true }),
			loading: false,
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("API service limits unavailable")).toBeInTheDocument();
		expect(screen.getByText("API Key sent over an unencrypted connection")).toBeInTheDocument();
		expect(screen.queryByText("$75.00")).not.toBeInTheDocument();
	});

	it("renders compact unlimited and window summaries", () => {
		render(ProfileQuotaSummary, {
			quota: quota({
				sub2api_snapshot: { ...quota().sub2api_snapshot!, unlimited: true },
			}),
			loading: false,
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 1, 0),
		}, { wrapper: TestProviders });

		expect(screen.getByText("Remaining: Unlimited")).toBeInTheDocument();
		expect(screen.getByText("5 hours")).toBeInTheDocument();
	});

	it("uses API service failure labels in compact summaries", () => {
		render(ProfileQuotaSummary, {
			quota: quota({ status: "auth_required", sub2api_snapshot: null }),
			loading: false,
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 1, 0),
		}, { wrapper: TestProviders });
		expect(screen.getByText("API Key rejected")).toHaveClass("text-destructive");
	});
});
