<script lang="ts">
	import { _ } from "svelte-i18n";
	import AlertTriangleIcon from "@lucide/svelte/icons/triangle-alert";
	import ArrowRightIcon from "@lucide/svelte/icons/arrow-right";
	import ShieldCheckIcon from "@lucide/svelte/icons/shield-check";

	import * as Accordion from "$lib/components/ui/accordion";
	import * as Alert from "$lib/components/ui/alert";
	import * as Dialog from "$lib/components/ui/dialog";
	import { Badge } from "$lib/components/ui/badge";
	import { Button } from "$lib/components/ui/button";
	import { Skeleton } from "$lib/components/ui/skeleton";
	import { Spinner } from "$lib/components/ui/spinner";
	import { joinUserMessages, switchWarningMessage } from "$lib/user-facing-messages";

	import type { PlanOperation, SwitchPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import type { SwitchProfileItem } from "./types";

	let {
		open = $bindable(false),
		profile,
		agent = "Codex",
		mode = "codex",
		currentProfile,
		plan,
		building,
		applying,
		inlineError,
		onClose,
		onConfirm,
	}: {
		open?: boolean;
		profile: SwitchProfileItem | null;
		agent?: string;
		mode?: "codex" | "antigravity";
		currentProfile: string;
		plan: SwitchPlan | null;
		building: boolean;
		applying: boolean;
		inlineError: string;
		onClose: () => void;
		onConfirm: () => void;
	} = $props();

	let operations = $derived(plan?.operations ?? []);
	let fileOperations = $derived(operations.filter((operation) => operation.backend_id === "file"));
	// Unknown non-file backends stay on the safe summary-only path. Their public
	// plans may intentionally omit locators, hashes, and previews.
	let sensitiveOperations = $derived(operations.filter((operation) => operation.backend_id !== "file"));
	let unsupportedCount = $derived(operations.filter((operation) => operation.action === "unsupported").length);
	let loginChanges = $derived(!!plan?.bindings?.find((binding) => binding.target_id === "auth")?.changed);
	let configChanges = $derived(!!plan?.bindings?.find((binding) => binding.target_id === "config")?.changed);
	let changeSummary = $derived(mode === "antigravity"
		? (sensitiveOperations.some((operation) => operation.action === "create" || operation.action === "update") ? $_("antigravity.use.loginChange") : $_("antigravity.use.loginSame"))
		: (loginChanges && configChanges ? $_("useDialog.bothChange") : loginChanges ? $_("useDialog.loginOnly") : configChanges ? $_("useDialog.configOnly") : $_("useDialog.sameBindings")));

	function handleOpenChange(value: boolean) {
		if (!value && applying) {
			open = true;
			return;
		}
		open = value;
		if (!value) onClose();
	}

	function operationWarnings(operation: PlanOperation): string[] {
		return [...new Set((operation.warnings ?? []).map(switchWarningMessage))];
	}

	function targetLabel(operation: PlanOperation): string {
		if (mode === "antigravity" && operation.backend_id === "keyring") return $_("antigravity.use.targetLabel");
		if (operation.target_id === "auth") return $_("useDialog.loginFile");
		if (operation.target_id === "config") return $_("useDialog.settingsFile");
		if (operation.target_label) return operation.target_label;
		return $_("useDialog.codexFile");
	}

	function sensitiveStatus(operation: PlanOperation): string {
		if (operation.action === "create") return $_("antigravity.use.create");
		if (operation.action === "update") return $_("antigravity.use.update");
		if (operation.action === "noop") return $_("antigravity.use.noop");
		return mode === "antigravity" ? $_("antigravity.use.unsupported") : $_("useDialog.unsupported");
	}

	function preview(operation: PlanOperation, side: "before" | "after"): string {
		const value = side === "before" ? operation.before_preview : operation.after_preview;
		if (!value?.content) return "—";
		return value.truncated ? `${value.content}\n${$_("useDialog.truncated")}` : value.content;
	}

	function actionLabel(action: string): string {
		const labels: Record<string, string> = {
			create: $_("planActions.create"),
			update: $_("planActions.update"),
			noop: $_("planActions.noop"),
			unsupported: $_("planActions.unsupported"),
		};
		return labels[action] ?? action;
	}
</script>

<Dialog.Root {open} onOpenChange={handleOpenChange}>
	<Dialog.Content class="max-h-[86vh] overflow-hidden sm:max-w-[760px]" showCloseButton={false}>
		<Dialog.Header>
			<Dialog.Title>
				{$_("useDialog.title", { values: { profile: profile?.name ?? profile?.id ?? "—", agent } })}
			</Dialog.Title>
			<Dialog.Description>{mode === "antigravity" ? $_("antigravity.use.description") : $_("useDialog.description", { values: { agent } })}</Dialog.Description>
		</Dialog.Header>

		<div class="flex min-h-0 flex-col gap-4 overflow-auto pr-1">
			<div class="flex items-center gap-3 rounded-lg border px-3 py-2 text-sm">
				<span class="min-w-0 flex-1 truncate font-mono text-muted-foreground">{currentProfile || $_("profile.noActive")}</span>
				<ArrowRightIcon data-icon="inline-start" class="shrink-0 text-muted-foreground" />
				<span class="min-w-0 flex-1 truncate text-right font-mono">{profile?.id ?? "—"}</span>
			</div>
			{#if plan}
				<Alert.Root>
					<ShieldCheckIcon data-icon="inline-start" />
					<Alert.Title>{changeSummary}</Alert.Title>
					<Alert.Description>{mode === "antigravity" ? $_("antigravity.use.summaryDescription") : $_("useDialog.changeSummaryDescription")}</Alert.Description>
				</Alert.Root>
			{:else}
				<Skeleton class="h-16 w-full" />
			{/if}

			{#if plan?.state_captures?.length}
			<Alert.Root>
				<ShieldCheckIcon data-icon="inline-start" />
				<Alert.Title>{mode === "antigravity" ? $_("antigravity.use.captureTitle") : $_("useDialog.captureTitle")}</Alert.Title>
				<Alert.Description>{mode === "antigravity" ? $_("antigravity.use.captureDescription") : $_("useDialog.captureDescription", { values: { count: plan.state_captures.length } })}</Alert.Description>
			</Alert.Root>
			{/if}

			{#if inlineError}
				<Alert.Root>
					<AlertTriangleIcon data-icon="inline-start" />
					<Alert.Title>{$_("useDialog.reviewAgain")}</Alert.Title>
					<Alert.Description>{inlineError}</Alert.Description>
				</Alert.Root>
			{/if}

			{#if plan?.warnings?.length}
				<Alert.Root>
					<AlertTriangleIcon data-icon="inline-start" />
					<Alert.Title>{$_("useDialog.planWarnings")}</Alert.Title>
					<Alert.Description>{joinUserMessages(plan.warnings, switchWarningMessage)}</Alert.Description>
				</Alert.Root>
			{/if}

			{#if unsupportedCount > 0}
				<Alert.Root variant="destructive">
					<AlertTriangleIcon data-icon="inline-start" />
					<Alert.Title>{$_("useDialog.unsupportedTitle")}</Alert.Title>
					<Alert.Description>{$_("useDialog.unsupported")}</Alert.Description>
				</Alert.Root>
			{/if}

			{#if building}
				<div class="flex flex-col gap-3">
					<Skeleton class="h-12 w-full" />
					<Skeleton class="h-12 w-full" />
				</div>
			{:else if plan && operations.length === 0}
				<Alert.Root>
					<ShieldCheckIcon data-icon="inline-start" />
					<Alert.Title>{$_("useDialog.noChangesTitle")}</Alert.Title>
					<Alert.Description>{$_("useDialog.noChanges")}</Alert.Description>
				</Alert.Root>
			{:else if plan}
				{#each sensitiveOperations as operation (`${operation.backend_id}:${operation.target_id}:${operation.target_label}`)}
					<Alert.Root>
						<ShieldCheckIcon data-icon="inline-start" />
						<Alert.Title>
							<span class="flex items-center gap-2">
								<Badge variant={operation.action === "unsupported" ? "destructive" : operation.action === "noop" ? "secondary" : "outline"}>{actionLabel(operation.action)}</Badge>
								<span>{targetLabel(operation)}</span>
							</span>
						</Alert.Title>
						<Alert.Description>{sensitiveStatus(operation)}</Alert.Description>
					</Alert.Root>
				{/each}
				{#if fileOperations.length}
				<Accordion.Root type="multiple">
					{#each fileOperations as operation (`${operation.target_id}:${operation.path}`)}
						<Accordion.Item value={`${operation.target_id}:${operation.path}`}>
							<Accordion.Trigger>
								<div class="flex min-w-0 flex-1 items-center gap-2 pr-2">
									<Badge variant={operation.action === "unsupported" ? "destructive" : operation.action === "noop" ? "secondary" : "outline"}>
										{actionLabel(operation.action)}
									</Badge>
									<span class="shrink-0 font-medium">{targetLabel(operation)}</span>
									<span class="truncate font-mono text-xs text-muted-foreground">{operation.path}</span>
								</div>
							</Accordion.Trigger>
							<Accordion.Content>
								<div class="grid gap-3 md:grid-cols-2">
									<div class="flex min-w-0 flex-col gap-2">
										<div class="text-xs font-medium text-muted-foreground">{$_("useDialog.before")}</div>
										<pre class="max-h-48 overflow-auto rounded-lg bg-muted p-3 text-xs">{preview(operation, "before")}</pre>
									</div>
									<div class="flex min-w-0 flex-col gap-2">
										<div class="text-xs font-medium text-muted-foreground">{$_("useDialog.after")}</div>
										<pre class="max-h-48 overflow-auto rounded-lg bg-muted p-3 text-xs">{preview(operation, "after")}</pre>
									</div>
								</div>
								{#if operationWarnings(operation).length}
									<Alert.Root class="mt-3">
										<AlertTriangleIcon data-icon="inline-start" />
										<Alert.Title>{$_("useDialog.operationWarnings")}</Alert.Title>
										<Alert.Description>{operationWarnings(operation).join(" ")}</Alert.Description>
									</Alert.Root>
								{/if}
							</Accordion.Content>
						</Accordion.Item>
					{/each}
				</Accordion.Root>
				{/if}
			{/if}
		</div>

		<Dialog.Footer>
			<Button variant="outline" disabled={applying} onclick={() => handleOpenChange(false)}>{$_("actions.cancel")}</Button>
			<Button disabled={building || !plan || unsupportedCount > 0 || applying} onclick={onConfirm}>
				{#if applying}<Spinner data-icon="inline-start" />{/if}
				{$_("actions.useProfile")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
