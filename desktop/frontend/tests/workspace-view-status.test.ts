import { render, screen } from "@testing-library/svelte";
import { locale } from "svelte-i18n";
import { tick } from "svelte";
import { describe, expect, it } from "vitest";

import WorkspaceViewStatus from "../src/features/workspace/WorkspaceViewStatus.svelte";

describe("WorkspaceViewStatus", () => {
	it("gives the loading state a localized accessible name", () => {
		render(WorkspaceViewStatus, { state: "loading" });

		expect(screen.getByRole("status", { name: "Loading page" })).toBeInTheDocument();
	});

	it("shows only fixed recovery guidance when loading fails", () => {
		const props = {
			state: "error" as const,
			error: "access_token=internal-sensitive-value",
		};
		render(WorkspaceViewStatus, props);

		const alert = screen.getByRole("alert");
		expect(alert).toHaveTextContent("Unable to open this page");
		expect(alert).toHaveTextContent("Close and reopen ProfileDeck, then try again.");
		expect(alert).not.toHaveTextContent(props.error);
	});

	it("provides the Simplified Chinese loading and error copy", async () => {
		locale.set("zh-CN");
		await tick();
		const { rerender } = render(WorkspaceViewStatus, { state: "loading" });

		expect(screen.getByRole("status", { name: "正在加载页面" })).toBeInTheDocument();
		await rerender({ state: "error" });
		expect(screen.getByRole("alert")).toHaveTextContent("无法打开此页面");
		expect(screen.getByRole("alert")).toHaveTextContent("请关闭并重新打开 ProfileDeck，然后重试。");
	});
});
