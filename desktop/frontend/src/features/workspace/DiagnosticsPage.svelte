<script lang="ts">
	import { _ } from "svelte-i18n";
	import CheckCircleIcon from "@lucide/svelte/icons/circle-check";
	import ShieldAlertIcon from "@lucide/svelte/icons/shield-alert";
	import TriangleAlertIcon from "@lucide/svelte/icons/triangle-alert";

	import type { DoctorResult } from "../../../bindings/github.com/strahe/profiledeck/internal/app/models";
	import ContentContainer from "$lib/components/app/ContentContainer.svelte";
	import OperationTable from "$lib/components/app/OperationTable.svelte";
	import SectionCard from "$lib/components/app/SectionCard.svelte";
	import * as Alert from "$lib/components/ui/alert";
	import * as AlertDialog from "$lib/components/ui/alert-dialog";
	import { Badge } from "$lib/components/ui/badge";
	import { Button, buttonVariants } from "$lib/components/ui/button";
	import { Skeleton } from "$lib/components/ui/skeleton";

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
				{#each findings as finding (finding.id)}
					<div class="flex items-start gap-3 rounded-lg border p-3">
						<Badge variant={findingVariant(finding.level)}>{finding.level}</Badge>
						<div class="min-w-0">
							<div class="text-sm font-medium">{finding.id}</div>
							<p class="mt-1 text-sm text-muted-foreground">{finding.message}</p>
						</div>
					</div>
				{/each}
			</SectionCard>
		{/if}

		{#if lockNeedsAttention}
			<SectionCard title={$_("diagnosticsPage.lock.title")} description={doctor.lock.reason}>
				<div class="flex flex-wrap items-center justify-between gap-3">
					<div class="flex items-center gap-2 text-sm">
						<ShieldAlertIcon class="size-4 text-muted-foreground" />
						<Badge variant={doctor.lock.level.toLowerCase() === "error" ? "destructive" : "secondary"}>{doctor.lock.level}</Badge>
						<span class="text-muted-foreground">{doctor.lock.pid_state} · {doctor.lock.os_lock_state}</span>
					</div>
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
			<SectionCard title={$_("diagnosticsPage.operations")} help={$_("diagnosticsPage.operationsDescription")} contentClass="px-0">
				<OperationTable {operations} {actionBusy} {onRecover} />
			</SectionCard>
		{/if}
	{/if}
</ContentContainer>
