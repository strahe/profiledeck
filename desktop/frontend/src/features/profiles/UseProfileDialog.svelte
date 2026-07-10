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

	import type { PlanOperation, SwitchPlan } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import type { CodexProfileListItem } from "./types";

	let {
		open = $bindable(false),
		profile,
		currentProfile,
		plan,
		building,
		applying,
		inlineError,
		onClose,
		onConfirm,
	}: {
		open?: boolean;
		profile: CodexProfileListItem | null;
		currentProfile: string;
		plan: SwitchPlan | null;
		building: boolean;
		applying: boolean;
		inlineError: string;
		onClose: () => void;
		onConfirm: () => void;
	} = $props();

	let operations = $derived(plan?.operations ?? []);
	let unsupportedCount = $derived(operations.filter((operation) => operation.action === "unsupported").length);

	function handleOpenChange(value: boolean) {
		open = value;
		if (!value) onClose();
	}

	function operationWarnings(operation: PlanOperation): string[] {
		return operation.warnings ?? [];
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
				{$_("useDialog.title", { values: { profile: profile?.name ?? profile?.id ?? "—", agent: "Codex" } })}
			</Dialog.Title>
			<Dialog.Description>{$_("useDialog.description", { values: { agent: "Codex" } })}</Dialog.Description>
		</Dialog.Header>

		<div class="flex min-h-0 flex-col gap-4 overflow-auto pr-1">
			<div class="flex items-center gap-3 rounded-lg border px-3 py-2 text-sm">
				<span class="min-w-0 flex-1 truncate font-mono text-muted-foreground">{currentProfile || $_("profile.noActive")}</span>
				<ArrowRightIcon data-icon="inline-start" class="shrink-0 text-muted-foreground" />
				<span class="min-w-0 flex-1 truncate text-right font-mono">{profile?.id ?? "—"}</span>
			</div>

			<Alert.Root>
				<ShieldCheckIcon data-icon="inline-start" />
				<Alert.Title>{$_("useDialog.safetyTitle")}</Alert.Title>
				<Alert.Description>{$_("useDialog.safetyDescription")}</Alert.Description>
			</Alert.Root>

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
					<Alert.Description>{plan.warnings.join(" ")}</Alert.Description>
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
				<Accordion.Root type="multiple">
					{#each operations as operation (`${operation.target_id}:${operation.path}`)}
						<Accordion.Item value={`${operation.target_id}:${operation.path}`}>
							<Accordion.Trigger>
								<div class="flex min-w-0 flex-1 items-center gap-2 pr-2">
									<Badge variant={operation.action === "unsupported" ? "destructive" : operation.action === "noop" ? "secondary" : "outline"}>
										{actionLabel(operation.action)}
									</Badge>
									<span class="shrink-0 font-medium">{operation.target_id}</span>
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
		</div>

		<Dialog.Footer>
			<Button variant="outline" onclick={() => handleOpenChange(false)}>{$_("actions.cancel")}</Button>
			<Button disabled={building || !plan || unsupportedCount > 0 || applying} onclick={onConfirm}>
				{#if applying}<Spinner data-icon="inline-start" />{/if}
				{$_("actions.useProfile")}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
