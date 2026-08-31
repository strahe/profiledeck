import { render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import { locale } from "svelte-i18n";
import { describe, expect, it, vi } from "vitest";

import type { GrokBuildProfileQuota } from "../bindings/github.com/strahe/profiledeck/internal/grokbuild/models";
import GrokBuildQuotaCard from "../src/features/profiles/GrokBuildQuotaCard.svelte";
import GrokBuildQuotaSummary from "../src/features/profiles/GrokBuildQuotaSummary.svelte";
import TestProviders from "./TestProviders.svelte";

const availableQuota: GrokBuildProfileQuota = {
	profile_id: "work",
	credential_id: "credential-work",
	config_set_id: "config-work",
	status: "available",
	snapshot: {
		fetched_at_unix_ms: Date.UTC(2026, 7, 2, 1, 0, 0),
		remaining_percent: 62.5,
		period_type: "USAGE_PERIOD_TYPE_WEEKLY",
		reset_at_unix_seconds: Date.UTC(2026, 7, 9, 1, 0, 0) / 1000,
		included_limit_cents: 10_000,
		included_used_cents: 3_750,
		prepaid_balance_cents: 1_250,
		on_demand_used_cents: 300,
		on_demand_cap_cents: 5_000,
		on_demand_enabled: true,
		subscription_tier: "SuperGrok Heavy",
	},
};

function renderCard(overrides: Partial<{
	quota: GrokBuildProfileQuota | null;
	loading: boolean;
	active: boolean;
	checkedAtUnixMS: number;
	checkOutcome: "updated" | "failed" | "never";
	nowUnixMS: number;
	disabled: boolean;
	onRefresh: () => void;
}> = {}) {
	return render(GrokBuildQuotaCard, {
		quota: null,
		loading: false,
		active: true,
		checkedAtUnixMS: 0,
		checkOutcome: "never",
		nowUnixMS: Date.UTC(2026, 7, 2, 1, 1, 0),
		disabled: false,
		onRefresh: vi.fn(),
		...overrides,
	}, { wrapper: TestProviders });
}

describe("Grok Build credits components", () => {
	it("shows included-only credits in the Profile list summary", () => {
		render(GrokBuildQuotaSummary, {
			quota: {
				...availableQuota,
				snapshot: {
					fetched_at_unix_ms: availableQuota.snapshot!.fetched_at_unix_ms,
					included_limit_cents: 10_000,
					included_used_cents: 3_750,
				},
			},
			loading: false,
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 1, 0),
		}, { wrapper: TestProviders });

		expect(screen.getByText("Included credits used / limit: $37.50 / $100.00")).toBeInTheDocument();
		expect(screen.queryByText("No credits to display")).not.toBeInTheDocument();
	});

	it("shows a runtime-unavailable Badge in the Profile list summary", () => {
		render(GrokBuildQuotaSummary, {
			quota: { ...availableQuota, status: "runtime_unavailable", snapshot: null },
			loading: false,
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 1, 0),
		}, { wrapper: TestProviders });

		expect(screen.getByText("Cannot start Grok Build")).toBeInTheDocument();
	});

	it("shows loading and a complete current-Profile credits summary", async () => {
		const view = renderCard({ loading: true });
		expect(document.querySelectorAll("[data-slot=skeleton]")).toHaveLength(2);
		expect(screen.getByRole("button", { name: "Refresh credits" })).toBeDisabled();

		await view.rerender({
			quota: availableQuota,
			loading: false,
			active: true,
			checkedAtUnixMS: availableQuota.snapshot!.fetched_at_unix_ms,
			checkOutcome: "updated",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 1, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("SuperGrok Heavy")).toBeInTheDocument();
		expect(screen.getByText("62.5% left")).toBeInTheDocument();
		expect(screen.getByText("Included credits used / limit")).toBeInTheDocument();
		expect(screen.getByText("Prepaid balance")).toBeInTheDocument();
		expect(screen.getByText("On-demand credits")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Refresh credits" })).toBeEnabled();
	});

	it("does not offer refresh for an inactive Profile and labels its cached snapshot", () => {
		renderCard({
			quota: { ...availableQuota, profile_id: "personal", status: "inactive" },
			active: false,
		});

		expect(screen.getByText("Session snapshot")).toBeInTheDocument();
		expect(screen.getByText("This result was checked earlier in this session. Use this Profile to refresh it.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Refresh credits" })).not.toBeInTheDocument();
	});

	it("shows authentication and unavailable states without exposing internal details", async () => {
		const view = renderCard({
			quota: { ...availableQuota, status: "auth_required", snapshot: null },
		});
		expect(screen.getByText("Sign in again to check credits")).toBeInTheDocument();
		expect(screen.getByText("Sign in to Grok Build again, then retry.")).toBeInTheDocument();

		await view.rerender({
			quota: { ...availableQuota, status: "runtime_unavailable", snapshot: null },
			loading: false,
			active: true,
			checkedAtUnixMS: Date.UTC(2026, 7, 2, 1, 2, 0),
			checkOutcome: "failed",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 3, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("Cannot start Grok Build")).toBeInTheDocument();
		expect(screen.getByText("Update or reinstall Grok Build, then retry.")).toBeInTheDocument();

		await view.rerender({
			quota: { ...availableQuota, status: "runtime_unavailable" },
			loading: false,
			active: true,
			checkedAtUnixMS: Date.UTC(2026, 7, 2, 1, 3, 0),
			checkOutcome: "failed",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 4, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("Cannot start Grok Build")).toBeInTheDocument();
		expect(screen.getByText("The latest check could not start Grok Build. The previous result from this session is still shown.")).toBeInTheDocument();
		expect(screen.getByText("62.5% left")).toBeInTheDocument();

		await view.rerender({
			quota: { ...availableQuota, status: "unavailable" },
			loading: false,
			active: true,
			checkedAtUnixMS: Date.UTC(2026, 7, 2, 1, 2, 0),
			checkOutcome: "failed",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 3, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("Credits unavailable")).toBeInTheDocument();
		expect(screen.getByText(/previous result from this session is still shown/)).toBeInTheDocument();
		expect(screen.getByText("62.5% left")).toBeInTheDocument();

		await view.rerender({
			quota: { ...availableQuota, status: "unsupported" },
			loading: false,
			active: true,
			checkedAtUnixMS: Date.UTC(2026, 7, 2, 1, 4, 0),
			checkOutcome: "failed",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 5, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("Credits check unsupported")).toBeInTheDocument();
		expect(screen.getByText(/not supported by this Grok Build setup/)).toBeInTheDocument();
		expect(screen.getByText("62.5% left")).toBeInTheDocument();
	});

	it("provides Simplified Chinese status and recovery copy", async () => {
		locale.set("zh-CN");
		await tick();
		const view = renderCard({
			quota: { ...availableQuota, status: "auth_required", snapshot: null },
		});

		expect(screen.getByText("重新登录后才能检查 credits")).toBeInTheDocument();
		expect(screen.getByText("请重新登录 Grok Build，然后重试。")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "刷新 credits" })).toBeInTheDocument();

		await view.rerender({
			quota: { ...availableQuota, status: "runtime_unavailable", snapshot: null },
			loading: false,
			active: true,
			checkedAtUnixMS: Date.UTC(2026, 7, 2, 1, 2, 0),
			checkOutcome: "failed",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 3, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("无法启动 Grok Build")).toBeInTheDocument();
		expect(screen.getByText("请更新或重新安装 Grok Build，然后重试。")).toBeInTheDocument();

		await view.rerender({
			quota: { ...availableQuota, status: "runtime_unavailable" },
			loading: false,
			active: true,
			checkedAtUnixMS: Date.UTC(2026, 7, 2, 1, 3, 0),
			checkOutcome: "failed",
			nowUnixMS: Date.UTC(2026, 7, 2, 1, 4, 0),
			disabled: false,
			onRefresh: vi.fn(),
		});
		expect(screen.getByText("最新检查无法启动 Grok Build；当前仍显示本次运行中上一次成功的结果。")).toBeInTheDocument();
		expect(screen.getByText("剩余 62.5%")).toBeInTheDocument();
	});
});
