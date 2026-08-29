import { render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import { locale } from "svelte-i18n";
import { describe, expect, it } from "vitest";

import type { CodexProfileSettings } from "../bindings/github.com/strahe/profiledeck/internal/codex/models";
import ProfileAutomationSettings from "../src/features/settings/ProfileAutomationSettings.svelte";
import ProfileTestProviders from "./ProfileTestProviders.svelte";

function profile(authMode: string): CodexProfileSettings {
	return {
		profile_id: "work",
		profile_name: "Work",
		credential_reference_count: 1,
		quota_refresh_interval_seconds: 0,
		auth_keepalive_enabled: false,
		auth_mode: authMode,
		quota_supported: false,
		auth_keepalive_supported: false,
		updated_at_unix_ms: 1,
	};
}

describe("Codex settings auth modes", () => {
	it("shows supported file sign-ins as neutral and keeps unsupported controls disabled", () => {
		for (const [mode, label] of [
			["apikey", "API Key"],
			["agentIdentity", "Access token"],
			["personalAccessToken", "Access token"],
		] as const) {
			const view = render(ProfileAutomationSettings, { profile: profile(mode) }, { wrapper: ProfileTestProviders });
			const badge = screen.getByText(label);
			expect(badge).toHaveClass("border-border");
			expect(badge).not.toHaveClass("text-destructive");
			expect(screen.getByRole("button", { name: "Refresh limits automatically" })).toBeDisabled();
			expect(screen.getByRole("switch")).toBeDisabled();
			expect(screen.getByText("This Profile's sign-in method does not provide ChatGPT Codex limits.")).toBeInTheDocument();
			expect(screen.getByText("This sign-in method cannot be renewed automatically.")).toBeInTheDocument();
			view.unmount();
		}
	});

	it("keeps unknown sign-ins visibly unsupported", () => {
		render(ProfileAutomationSettings, { profile: profile("future") }, { wrapper: ProfileTestProviders });
		expect(screen.getByText("Unsupported sign-in")).toHaveClass("text-destructive");
	});

	it("provides Simplified Chinese labels and disabled reasons", async () => {
		locale.set("zh-CN");
		await tick();
		render(ProfileAutomationSettings, { profile: profile("agentIdentity") }, { wrapper: ProfileTestProviders });
		expect(screen.getByText("访问令牌")).toBeInTheDocument();
		expect(screen.getByText("此 Profile 的登录方式无法提供 ChatGPT Codex 限额。")).toBeInTheDocument();
		expect(screen.getByText("此登录方式无法自动续期。")).toBeInTheDocument();
	});
});
