import { render, screen, waitFor, within } from "@testing-library/svelte";
import { locale } from "svelte-i18n";
import { tick } from "svelte";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { DoctorOperation, DoctorResult } from "../bindings/github.com/strahe/profiledeck/internal/doctor/models";
import DiagnosticsPage from "../src/features/workspace/DiagnosticsPage.svelte";
import TestProviders from "./TestProviders.svelte";

const recoverableOperation: DoctorOperation = {
	id: "operation-correct-id",
	operation_type: "switch",
	status: "pending",
	profile_id: "profile-one",
	recovery_status: "recoverable",
	recovery_action: "restore",
	updated_at_unix_ms: 1,
	level: "warning",
	reason: "switch_incomplete",
};

function doctorResult(overrides: Partial<DoctorResult> = {}): DoctorResult {
	return {
		config_dir: "",
		runtime_root: "",
		database_path: "",
		overall_level: "warning",
		findings: [],
		operations: [],
		lock: {
			path: "",
			exists: false,
			pid_state: "absent",
			os_lock_state: "free",
			stale_candidate: false,
			repairable: false,
			level: "ok",
			reason: "not_present",
		},
		...overrides,
	};
}

function renderDiagnostics(doctor: DoctorResult, actionBusy = "") {
	const callbacks = {
		onRecheck: vi.fn(),
		onRepair: vi.fn(),
		onRetryCleanup: vi.fn(),
		onRecover: vi.fn(),
	};
	render(DiagnosticsPage, {
		doctor,
		loading: false,
		error: "",
		actionBusy,
		...callbacks,
	}, { wrapper: TestProviders });
	return callbacks;
}

describe("DiagnosticsPage confirmations", () => {
	it("shows permission guidance without backend error text", () => {
		const path = "/Users/alice/.config/profiledeck/recovery";
		const backendMessage = "chmod failed: SECRET_INTERNAL_ERROR";
		renderDiagnostics(doctorResult({
			findings: [{
				id: "recovery_permissions_weak",
				level: "error",
				message: backendMessage,
				details: { path, mode: "0755", want: "0700" },
			}],
		}));

		expect(screen.getByText("Sensitive ProfileDeck files may be visible to other users")).toBeInTheDocument();
		expect(screen.getByText(`Path: ${path}`)).toBeInTheDocument();
		expect(screen.queryByText(backendMessage)).not.toBeInTheDocument();
	});

	it("shows Simplified Chinese permission guidance", async () => {
		const path = "/Users/alice/.config/profiledeck/recovery";
		locale.set("zh-CN");
		await tick();
		renderDiagnostics(doctorResult({
			findings: [{
				id: "recovery_permissions_weak",
				level: "error",
				message: "chmod failed: SECRET_INTERNAL_ERROR",
				details: { path, mode: "0755", want: "0700" },
			}],
		}));

		expect(screen.getByText("其他系统用户可能可以访问 ProfileDeck 敏感文件")).toBeInTheDocument();
		expect(screen.getByText(`路径：${path}`)).toBeInTheDocument();
	});

	it("confirms recovery cleanup before invoking it", async () => {
		const user = userEvent.setup();
		const callbacks = renderDiagnostics(doctorResult({
			findings: [{
				id: "operation_recovery_cleanup_required",
				level: "warning",
				message: "internal cleanup detail",
			}],
		}));

		await user.click(screen.getByRole("button", { name: "Retry cleanup" }));
		expect(callbacks.onRetryCleanup).not.toHaveBeenCalled();
		const dialog = await screen.findByRole("alertdialog");
		expect(within(dialog).getByText("Retry recovery cleanup?")).toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Retry cleanup" }));
		expect(callbacks.onRetryCleanup).toHaveBeenCalledOnce();
		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
	});

	it("confirms an incomplete operation and forwards the exact operation ID", async () => {
		const user = userEvent.setup();
		const callbacks = renderDiagnostics(doctorResult({ operations: [recoverableOperation] }));

		await user.click(screen.getByRole("button", { name: "Restore pre-switch state" }));
		expect(callbacks.onRecover).not.toHaveBeenCalled();
		const dialog = await screen.findByRole("alertdialog");
		expect(within(dialog).getByText("Restore the pre-switch state?")).toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Restore pre-switch state" }));
		expect(callbacks.onRecover).toHaveBeenCalledWith("operation-correct-id");
		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
	});

	it("disables recovery actions while another action is busy", () => {
		renderDiagnostics(doctorResult({
			findings: [{
				id: "operation_recovery_cleanup_required",
				level: "warning",
				message: "internal cleanup detail",
			}],
			operations: [recoverableOperation],
		}), "doctor");

		expect(screen.getByRole("button", { name: "Retry cleanup" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Restore pre-switch state" })).toBeDisabled();
	});
});
