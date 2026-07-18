<script lang="ts">
	import { _ } from "svelte-i18n";
	import CheckCircleIcon from "@lucide/svelte/icons/circle-check";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import type { Finding as DoctorFinding, DoctorResult } from "../../../bindings/github.com/strahe/profiledeck/internal/doctor/models";
	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import OperationTable from "$lib/components/app/OperationTable.svelte";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { Badge } from "$lib/components/ui/badge";
	import { Button, buttonVariants } from "$lib/components/ui/button";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { translate } from "$lib/i18n";

	let {
		doctor,
		loading,
		error,
		actionBusy,
		onRecheck,
		onRepair,
		onRecover,
	}: {
		doctor: DoctorResult | null;
		loading: boolean;
		error: string;
		actionBusy: string;
		onRecheck: () => void | Promise<void>;
		onRepair: () => void | Promise<void>;
		onRecover: (operationID: string) => void | Promise<void>;
	} = $props();

	let findings = $derived((doctor?.findings ?? []).filter((finding) => finding.level.toLowerCase() !== "ok"));
	let operations = $derived((doctor?.operations ?? []).filter((operation) => operation.status === "pending" || operation.status === "failed"));
	let lockNeedsAttention = $derived(!!doctor?.lock && (doctor.lock.level.toLowerCase() !== "ok" || doctor.lock.repairable));
	let healthy = $derived(!!doctor && doctor.overall_level.toLowerCase() === "ok" && findings.length === 0 && operations.length === 0 && !lockNeedsAttention);

	function findingVariant(level: string): "destructive" | "secondary" | "outline" {
		const normalized = level.toLowerCase();
		if (normalized === "error") return "destructive";
		if (normalized === "warn" || normalized === "warning") return "secondary";
		return "outline";
	}

	const findingCopyByID: Record<string, string> = {
		database_not_initialized: "databaseNotInitialized",
		database_inspect_failed: "databaseUnavailable",
		database_open_failed: "databaseUnavailable",
		database_status_failed: "databaseUnavailable",
		database_schema_unhealthy: "databaseIntegrityInvalid",
		database_schema_unsupported: "databaseVersionUnsupported",
		database_upgrade_required: "databaseUpgradeRequired",
		database_quick_check_failed: "databaseIntegrityInvalid",
		database_foreign_key_check_failed: "databaseIntegrityInvalid",
		database_json_invalid: "databaseIntegrityInvalid",
		database_references_invalid: "databaseIntegrityInvalid",
		operation_list_failed: "operationCheckFailed",
		codex_provider_check_failed: "codexSetupUnavailable",
		codex_preset_v2_invalid: "codexSetupUnavailable",
		codex_binding_check_failed: "profileDataUnavailable",
		codex_config_binding_missing: "profileSettingsUnavailable",
		codex_config_binding_invalid: "profileSettingsUnavailable",
		codex_config_set_invalid: "profileSettingsUnavailable",
		codex_login_binding_missing: "profileLoginUnavailable",
		codex_login_binding_invalid: "profileLoginUnavailable",
		codex_login_state_invalid: "profileLoginUnavailable",
		codex_config_set_check_failed: "configSetsUnavailable",
		antigravity_provider_check_failed: "antigravitySetupUnavailable",
		antigravity_agy_v2_invalid: "antigravitySetupUnavailable",
		antigravity_binding_check_failed: "antigravityProfileDataUnavailable",
		antigravity_active_state_check_failed: "antigravityProfileDataUnavailable",
		antigravity_profile_missing: "antigravityProfileDataUnavailable",
		antigravity_login_binding_invalid: "antigravityProfileDataUnavailable",
		antigravity_login_state_invalid: "antigravityLoginUnavailable",
		antigravity_login_unavailable: "antigravityLoginUnavailable",
		antigravity_login_missing: "antigravityLoginUnavailable",
		antigravity_login_invalid: "antigravityLoginUnavailable",
		claude_code_provider_check_failed: "claudeCodeSetupUnavailable",
		claude_code_preset_invalid: "claudeCodeSetupUnavailable",
		claude_code_binding_check_failed: "claudeCodeProfileDataUnavailable",
		claude_code_active_state_check_failed: "claudeCodeProfileDataUnavailable",
		claude_code_profile_missing: "claudeCodeProfileDataUnavailable",
		claude_code_login_binding_invalid: "claudeCodeProfileDataUnavailable",
		claude_code_login_state_check_failed: "claudeCodeSavedLoginUnavailable",
		claude_code_login_state_invalid: "claudeCodeSavedLoginUnavailable",
		claude_code_login_unavailable: "claudeCodeWorkingLoginAttention",
		claude_code_login_missing: "claudeCodeWorkingLoginAttention",
		claude_code_login_symlink: "claudeCodeWorkingLoginAttention",
		claude_code_login_file_type: "claudeCodeWorkingLoginAttention",
		claude_code_keychain_reference_invalid: "claudeCodeWorkingLoginAttention",
		claude_code_keychain_authorization_required: "claudeCodeWorkingLoginAttention",
		claude_code_login_invalid: "claudeCodeWorkingLoginAttention",
		claude_code_login_unsupported: "claudeCodeWorkingLoginAttention",
		claude_code_expiry_unknown: "claudeCodeWorkingLoginAttention",
		claude_code_working_copy_changed: "claudeCodeWorkingLoginAttention",
		claude_code_credentials_permissions: "claudeCodeCredentialPermissions",
		claude_code_credentials_replace_unavailable: "claudeCodeCredentialPermissions",
		database_permissions_weak: "databasePermissions",
		backups_permissions_weak: "backupPermissions",
		codex_auth_target_permissions_weak: "loginPermissions",
		codex_auth_target_permission_check_failed: "permissionsCheckFailed",
		database_permissions_weak_inspect_failed: "permissionsCheckFailed",
		backups_permissions_weak_inspect_failed: "permissionsCheckFailed",
		codex_auth_target_permissions_weak_inspect_failed: "permissionsCheckFailed",
	};

	function findingCopyKey(finding: DoctorFinding): string {
		return findingCopyByID[finding.id] ?? "unknown";
	}

	function findingTitle(finding: DoctorFinding): string {
		return translate(`diagnosticsPage.finding.${findingCopyKey(finding)}Title`);
	}

	function findingDescription(finding: DoctorFinding): string {
		return translate(`diagnosticsPage.finding.${findingCopyKey(finding)}Description`);
	}

	function findingReference(finding: DoctorFinding): string {
		const profileID = String(finding.details?.profile_id ?? "");
		if (profileID) return translate("diagnosticsPage.finding.affectedProfile", { value: profileID });
		const configSetID = String(finding.details?.config_set_id ?? "");
		if (configSetID) return translate("diagnosticsPage.finding.affectedConfigSet", { value: configSetID });
		return "";
	}

	function levelLabel(level: string): string {
		return level.toLowerCase() === "error"
			? translate("diagnosticsPage.level.actionRequired")
			: translate("diagnosticsPage.level.warning");
	}

	function lockDescription(reason: string): string {
		if (["malformed_lock_file", "stale_lock_candidate", "applied_operation_lock_residue", "maintenance_lock_residue"].includes(reason)) {
			return translate("diagnosticsPage.lock.repairable");
		}
		if (["lock_may_be_active", "os_lock_not_free", "pending_operation"].includes(reason)) {
			return translate("diagnosticsPage.lock.active");
		}
		if (reason === "database_unavailable") return translate("diagnosticsPage.lock.databaseUnavailable");
		if (reason === "lock_file_read_failed") return translate("diagnosticsPage.lock.unreadable");
		return translate("diagnosticsPage.lock.unknown");
	}
</script>

<ContentContainer class="max-w-4xl">
	{#if error}
		<Alert.Root variant="destructive">
			<TriangleAlertIcon data-icon="inline-start" />
			<Alert.Title>{$_("diagnosticsPage.loadFailed")}</Alert.Title>
			<Alert.Description>{error}</Alert.Description>
			<Alert.Action><Button size="xs" variant="outline" onclick={onRecheck}>{$_("actions.retry")}</Button></Alert.Action>
		</Alert.Root>
	{:else if loading && !doctor}
		<div class="flex flex-col gap-3"><Skeleton class="h-20 w-full" /><Skeleton class="h-48 w-full" /></div>
	{:else if healthy}
		<Alert.Root>
			<CheckCircleIcon data-icon="inline-start" />
			<Alert.Title>{$_("diagnosticsPage.healthyTitle")}</Alert.Title>
			<Alert.Description>{$_("diagnosticsPage.healthyDescription")}</Alert.Description>
		</Alert.Root>
	{:else if doctor}
		{#if findings.length}
			<SectionCard title={$_("diagnosticsPage.findings")} contentClass="flex flex-col gap-3">
				{#each findings as finding (`${finding.id}:${finding.details?.profile_id ?? ""}:${finding.details?.config_set_id ?? ""}`)}
					<div class="flex items-start gap-3 rounded-lg border p-3">
						<Badge variant={findingVariant(finding.level)}>{levelLabel(finding.level)}</Badge>
						<div class="min-w-0">
							<div class="text-sm font-medium">{findingTitle(finding)}</div>
							<p class="mt-1 text-sm text-muted-foreground">{findingDescription(finding)}</p>
							{#if findingReference(finding)}<p class="mt-1 font-mono text-xs text-muted-foreground">{findingReference(finding)}</p>{/if}
						</div>
					</div>
				{/each}
			</SectionCard>
		{/if}

		{#if lockNeedsAttention}
			<SectionCard title={$_("diagnosticsPage.lock.title")} description={lockDescription(doctor.lock.reason)}>
				<div class="flex flex-wrap items-center justify-between gap-3">
					<Badge variant={doctor.lock.level.toLowerCase() === "error" ? "destructive" : "secondary"}>{levelLabel(doctor.lock.level)}</Badge>
					{#if doctor.lock.repairable}
						<AlertDialog.Root>
							<AlertDialog.Trigger disabled={!!actionBusy} class={buttonVariants({ variant: "outline", size: "sm" })}>
								{$_("actions.repairLock")}
							</AlertDialog.Trigger>
							<AlertDialog.Content>
								<AlertDialog.Header>
									<AlertDialog.Title>{$_("diagnosticsPage.lock.repairTitle")}</AlertDialog.Title>
									<AlertDialog.Description>{$_("diagnosticsPage.lock.repairDescription")}</AlertDialog.Description>
								</AlertDialog.Header>
								<AlertDialog.Footer>
									<AlertDialog.Cancel>{$_("actions.cancel")}</AlertDialog.Cancel>
									<AlertDialog.Action onclick={onRepair}>{$_("actions.repairLock")}</AlertDialog.Action>
								</AlertDialog.Footer>
							</AlertDialog.Content>
						</AlertDialog.Root>
					{/if}
				</div>
			</SectionCard>
		{/if}

		{#if operations.length}
			<SectionCard title={$_("diagnosticsPage.operations")} description={$_("diagnosticsPage.operationsDescription")} contentClass="px-0">
				<OperationTable {operations} {actionBusy} {onRecover} />
			</SectionCard>
		{/if}
	{/if}
</ContentContainer>
