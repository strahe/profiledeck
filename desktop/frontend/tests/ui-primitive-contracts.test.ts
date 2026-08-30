import { render, screen } from "@testing-library/svelte";
import { describe, expect, it } from "vitest";

import SettingsRow from "../src/lib/components/app/SettingsRow.svelte";
import { Input } from "../src/lib/components/ui/input";

describe("ProfileDeck UI primitive contracts", () => {
	it("disables text assistance by default for inputs", () => {
		render(Input, { "aria-label": "Profile value" });

		const input = screen.getByRole("textbox", { name: "Profile value" });
		expect(input).toHaveAttribute("autocapitalize", "none");
		expect(input).toHaveAttribute("autocorrect", "off");
		expect(input).toHaveAttribute("spellcheck", "false");
	});

	it("keeps SettingsRow descriptions compact", () => {
		render(SettingsRow, {
			label: "Quota refresh",
			description: "Refresh usage after switching Profiles.",
		});

		expect(screen.getByText("Refresh usage after switching Profiles.")).toHaveClass(
			"text-xs",
			"leading-relaxed",
		);
	});
});
