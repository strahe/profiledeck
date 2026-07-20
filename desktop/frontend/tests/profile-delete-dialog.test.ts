import { cleanup, render, screen, waitFor, within } from "@testing-library/svelte";
import userEvent from "@testing-library/user-event";
import { locale } from "svelte-i18n";
import { tick } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";

const backend = vi.hoisted(() => ({
	deleteProfile: vi.fn(),
}));

vi.mock("../bindings/github.com/strahe/profiledeck/desktop/backend", () => ({
	ProfileService: { Delete: backend.deleteProfile },
}));

import ProfileDeleteDialog from "../src/features/profiles/ProfileDeleteDialog.svelte";
import TestProviders from "./TestProviders.svelte";

const target = {
	id: "profile-complete-id-019f7afc",
	name: "Work Profile",
};

function renderDialog(onDeleted = vi.fn()) {
	render(ProfileDeleteDialog, {
		open: true,
		profile: target,
		onDeleted,
	}, { wrapper: TestProviders });
	return onDeleted;
}

afterEach(() => {
	cleanup();
	backend.deleteProfile.mockReset();
});

describe("ProfileDeleteDialog", () => {
	it("explains global cleanup and shows the complete Profile identity", async () => {
		const user = userEvent.setup();
		renderDialog();
		const dialog = await screen.findByRole("alertdialog");

		expect(within(dialog).getByText('Delete "Work Profile"?')).toBeInTheDocument();
		expect(within(dialog).getByText(`Profile ID: ${target.id}`)).toBeInTheDocument();
		expect(within(dialog).getByText(/removes the Profile from every Agent/)).toBeInTheDocument();
		expect(within(dialog).getByText(/Shared saved data will remain/)).toBeInTheDocument();
		expect(within(dialog).getByText(/Current tool logins, settings, and files will not change/)).toBeInTheDocument();

		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
		expect(backend.deleteProfile).not.toHaveBeenCalled();
	});

	it("stays open while busy and closes only after a successful delete", async () => {
		const user = userEvent.setup();
		let resolveDelete!: (value: { id: string; deleted: boolean }) => void;
		const pending = new Promise<{ id: string; deleted: boolean }>((resolve) => {
			resolveDelete = resolve;
		});
		backend.deleteProfile.mockReturnValueOnce(pending);
		const onDeleted = renderDialog();
		const dialog = await screen.findByRole("alertdialog");
		const deleteButton = within(dialog).getByRole("button", { name: "Delete Profile" });

		await user.click(deleteButton);
		expect(backend.deleteProfile).toHaveBeenCalledWith(target.id, true);
		expect(deleteButton).toBeDisabled();
		expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
		await user.click(deleteButton);
		expect(backend.deleteProfile).toHaveBeenCalledOnce();

		resolveDelete({ id: target.id, deleted: true });
		await waitFor(() => expect(onDeleted).toHaveBeenCalledWith(target));
		await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
	});

	it.each([
		["active", "This Profile is current in at least one Agent. Use another Profile there, then try again."],
		["unresolved_operation", "This Profile has an unfinished operation. Resolve it in Diagnostics, then try again."],
		["unsupported_managed_data", "This Profile contains unsupported saved data. Remove or repair that data, then try again."],
	])("keeps the dialog open with recovery guidance for %s", async (reason, message) => {
		const user = userEvent.setup();
		backend.deleteProfile.mockRejectedValueOnce({
			code: "PROFILE_IN_USE",
			message: "backend detail that must not be shown",
			details: { reason },
		});
		const onDeleted = renderDialog();
		const dialog = await screen.findByRole("alertdialog");

		await user.click(within(dialog).getByRole("button", { name: "Delete Profile" }));
		expect(await within(dialog).findByText(message)).toBeInTheDocument();
		expect(within(dialog).queryByText(/backend detail/)).not.toBeInTheDocument();
		expect(screen.getByRole("alertdialog")).toBeInTheDocument();
		expect(onDeleted).not.toHaveBeenCalled();
	});

	it("uses localized Simplified Chinese confirmation copy", async () => {
		locale.set("zh-CN");
		await tick();
		renderDialog();
		const dialog = await screen.findByRole("alertdialog");

		expect(within(dialog).getByText("删除“Work Profile”？")).toBeInTheDocument();
		expect(within(dialog).getByText(`Profile ID：${target.id}`)).toBeInTheDocument();
		expect(within(dialog).getByText(/从 ProfileDeck 的所有 Agent 中删除/)).toBeInTheDocument();
	});
});
