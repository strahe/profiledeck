package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/doctor"
)

func newDoctorCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "doctor",
		Usage: "Diagnose ProfileDeck runtime state",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Commands: []*urfavecli.Command{
			newDoctorRepairLockCommand(),
			newDoctorRetryCleanupCommand(),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Doctor().Run(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeDoctorResult(outputWriter(cmd), result)
		},
	}
}

func newDoctorRetryCleanupCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "retry-cleanup",
		Usage: "Retry cleanup of temporary operation recovery files",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm recovery cleanup"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Doctor().RetryRecoveryCleanup(ctx, cmd.Bool(yesFlagName))
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintln(
				outputWriter(cmd),
				"Recovery cleanup completed\nSwitching and application restore are available. Tool sign-ins and settings were not changed.",
			)
			return err
		},
	}
}

func newDoctorRepairLockCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "repair-lock",
		Usage: "Remove a clearly stale switch lock file",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm stale lock repair"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Doctor().RepairLock(ctx, cmd.Bool(yesFlagName))
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Lock repaired\npath: %s\nreason: %s\n", result.Path, result.Reason)
			return err
		},
	}
}

func writeDoctorResult(w io.Writer, result doctor.DoctorResult) error {
	if _, err := fmt.Fprintf(
		w,
		"ProfileDeck doctor\noverall: %s\nconfig dir: %s\nruntime root: %s\n",
		result.OverallLevel,
		result.ConfigDir,
		result.RuntimeRoot,
	); err != nil {
		return err
	}
	if err := writeDoctorFindings(w, result.Findings); err != nil {
		return err
	}
	if err := writeDoctorOperations(w, result.Operations); err != nil {
		return err
	}
	return writeDoctorLock(w, result.Lock)
}

func writeDoctorFindings(w io.Writer, findings []doctor.Finding) error {
	if _, err := fmt.Fprintln(w, "database:"); err != nil {
		return err
	}
	for _, finding := range findings {
		if _, err := fmt.Fprintf(w, "  %s %s: %s\n", finding.Level, finding.ID, finding.Message); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctorOperations(w io.Writer, operations []doctor.DoctorOperation) error {
	if _, err := fmt.Fprintln(w, "operations:"); err != nil {
		return err
	}
	if len(operations) == 0 {
		_, err := fmt.Fprintln(w, "  OK no incomplete operations")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, operation := range operations {
		errorDetails := doctorOperationErrorDetails(operation)
		if _, err := fmt.Fprintf(
			tw,
			"  %s\t%s\t%s\t%s\tcheckpoint=%s%s\treason=%s\n",
			operation.Level,
			operation.ID,
			operation.OperationType,
			operation.Status,
			operation.Checkpoint,
			errorDetails,
			operation.Reason,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func doctorOperationErrorDetails(operation doctor.DoctorOperation) string {
	errorCode := doctorOutputField(operation.ErrorCode)
	if errorCode == "" {
		errorCode = "-"
	}
	errorMessage := doctorOutputField(operation.ErrorMessage)
	if errorMessage == "" {
		errorMessage = "-"
	}
	details := fmt.Sprintf("\terror_code=%s\terror_message=%s", errorCode, errorMessage)
	if operation.RecoveryStatus != "" {
		details += fmt.Sprintf("\trecovery_status=%s", doctorOutputField(operation.RecoveryStatus))
	}
	if operation.RecoveryAction != "" {
		details += fmt.Sprintf("\trecovery_action=%s", doctorOutputField(operation.RecoveryAction))
	}
	if operation.RecoveryReason != "" {
		details += fmt.Sprintf("\trecovery_reason=%s", doctorOutputField(operation.RecoveryReason))
	}
	return details
}

func doctorOutputField(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return value
}

func writeDoctorLock(w io.Writer, lock doctor.DoctorLock) error {
	if _, err := fmt.Fprintln(w, "lock:"); err != nil {
		return err
	}
	if !lock.Exists {
		_, err := fmt.Fprintf(w, "  %s %s\n", lock.Level, lock.Reason)
		return err
	}
	_, err := fmt.Fprintf(
		w,
		"  %s owner=%s pid=%d pid_state=%s os_lock=%s operation=%s operation_status=%s repairable=%t reason=%s\n",
		lock.Level,
		lock.Owner,
		lock.PID,
		lock.PIDState,
		lock.OSLockState,
		lock.OperationID,
		lock.OperationStatus,
		lock.Repairable,
		lock.Reason,
	)
	return err
}
