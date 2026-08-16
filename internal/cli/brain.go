package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/lima"
)

type brainService interface {
	Init(context.Context) (brain.InitReport, error)
	Status(context.Context) (brain.StatusReport, error)
	Import(context.Context, brain.ImportOptions) (brain.TransferReport, error)
	Sync(context.Context) (brain.SyncReport, error)
}

func newBrainCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Initialize and inspect the private Markdown Second Brain",
		// No instance name and no vault path here. Both are resolved per
		// invocation from the backend the instance runs, and `--help` never
		// reaches that resolution — so a line built at construction time told a
		// Claude Code operator to copy from the other backend's box and the
		// other backend's directory, plausibly enough to try it.
		Long: "Manage the mandatory Torio Second Brain. It stays on the guest's native " +
			"filesystem, is private to the agent identity that owns it, and is registered " +
			"with the backend's project registry when it declares one. Commands report only " +
			"bounded aggregate metadata, never note names or content.\n\n" +
			"Torio brings data in and does not take it out. To copy the Brain back to the " +
			"host, run limactl yourself; `torio brain status` prints the instance and vault " +
			"path to copy from, and the exact command.\n\n" +
			"That is an operator command, not a Torio feature: nothing verifies the " +
			"result and Torio does not call it a backup.",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio brain --help'")
			}
			return usageError(fmt.Sprintf("unknown brain subcommand %q", args[0]))
		},
	}
	cmd.AddCommand(newBrainInitCmd(a))
	cmd.AddCommand(newBrainStatusCmd(a))
	cmd.AddCommand(newBrainImportCmd(a))
	cmd.AddCommand(newBrainSyncCmd(a))
	return cmd
}

func newBrainImportCmd(a *app) *cobra.Command {
	var dryRun bool
	var into string
	cmd := &cobra.Command{
		Use:   "import <host-directory>",
		Short: "Import a filtered Markdown vault into the Brain",
		Long: "Preflight and import allowlisted Markdown, Canvas, and local attachment files " +
			"through private host and guest staging. Credential-shaped files, repository " +
			"metadata, links, hardlinks, special files, and executables are refused or skipped. " +
			"Existing data is never overwritten except for the exact pristine Torio scaffold. " +
			"Use --into with a new contained subdirectory to avoid collisions. Output contains " +
			"only aggregate counts and a manifest digest, never note names or content.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.brainService("brain.import")
			if err != nil {
				return err
			}
			report, err := service.Import(ctx, brain.ImportOptions{
				Source: args[0],
				Into:   into,
				DryRun: dryRun,
			})
			if err != nil {
				cliErr := mapBrainError("brain.import", err)
				cliErr.Details = brainTransferDetails(report)
				return cliErr
			}
			return a.emitBrainTransfer("brain.import", report)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preflight and report without transferring or changing Brain data")
	cmd.Flags().StringVar(&into, "into", "", "import as one new relative subtree below the Brain")
	return cmd
}

func newBrainInitCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create or verify the private Git-versioned Brain",
		Long: "Create the canonical Markdown scaffold atomically through private guest staging, " +
			"and make its initial local Git commit. " +
			"Project. Once the Brain verifies, install or refresh the global torio-brain " +
			"retrieval skill so every project can search it; sessions already open must be " +
			"restarted to see it. Idempotent for matching managed state; refuses non-empty " +
			"unmanaged data. Does not configure a remote or push.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.brainService("brain.init")
			if err != nil {
				return err
			}
			report, err := service.Init(ctx)
			if err != nil {
				cliErr := mapBrainError("brain.init", err)
				cliErr.Details = brainStatusDetails(report.Status)
				return cliErr
			}
			return a.emitBrainInit(report)
		},
	}
}

func newBrainStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report Brain integrity and bounded aggregate metadata",
		Long: "Report initialized, uninitialized, or drift state; canonical path, native " +
			"filesystem, ownership/mode, Git worktree state, bounded aggregate counts and bytes, " +
			"and retrieval skill state. No note names or content " +
			"are returned.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.brainService("brain.status")
			if err != nil {
				return err
			}
			report, err := service.Status(ctx)
			if err != nil {
				cliErr := mapBrainError("brain.status", err)
				cliErr.Details = brainStatusDetails(report)
				return cliErr
			}
			// Written here rather than in help text because only a run knows
			// which box this is about, and what it should do next depends on
			// whether this replica has ever met the host vault.
			next := "reconcile this replica with the host vault: torio brain sync"
			if report.HubRefKnown && report.AheadOfHub == 0 && report.BehindHub == 0 {
				next = "this replica is level with the host vault"
			}
			notes := append(brainSkillNotes(report.SkillState, report.SkillPath, false), next)
			return a.emitBrainStatus("brain.status", report, notes)
		},
	}
}

// newBrainSyncCmd reconciles the bound guest's vault with the host's.
//
// There is one Second Brain and every backend's guest keeps a copy of it
// (ADR-0025). This is what makes two copies agree: each side writes a Git
// bundle, the transport carries the file, and the other side merges from it.
// Nothing gains a network remote and no note content is reported.
func newBrainSyncCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reconcile this backend's Brain replica with the one on the host",
		Long: "Carry this guest's vault to the host vault and the host vault back, using Git " +
			"bundles over the same one-shot transport `brain import` uses. Unsaved work in the " +
			"guest vault is committed first, because an agent writes notes and never commits " +
			"them.\n\n" +
			"Neither vault gains a network remote: a bundle is a file, read once and removed. A " +
			"merge that cannot be made automatically stops that direction, leaves it exactly as " +
			"it was, and names the host vault, where you resolve it with ordinary Git. Counts " +
			"are reported; note names and content are not.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Carrying a vault is bounded like every operation, but it is the
			// long kind: it commits, bundles, copies and merges.
			ctx, cancel := context.WithTimeout(cmd.Context(), config.MaxTimeout)
			defer cancel()
			service, err := a.brainService("brain.sync")
			if err != nil {
				return err
			}
			report, err := service.Sync(ctx)
			if err != nil {
				cliErr := mapBrainError("brain.sync", err)
				if len(report.Notes) > 0 {
					cliErr.Details = map[string]any{"notes": notes(report.Notes)}
				}
				return cliErr
			}
			return a.emitBrainSync(report)
		},
	}
}

type brainSyncData struct {
	HubPath          string   `json:"hub_path"`
	HubCreated       bool     `json:"hub_created"`
	Snapshotted      bool     `json:"snapshotted"`
	ToHub            int      `json:"commits_to_hub"`
	ToGuest          int      `json:"commits_to_guest"`
	ConflictOutbound bool     `json:"conflict_to_hub"`
	ConflictInbound  bool     `json:"conflict_to_guest"`
	Notes            []string `json:"notes"`
	NextStep         string   `json:"next_step"`
}

func (a *app) emitBrainSync(report brain.SyncReport) error {
	next := "torio brain status"
	if report.Conflicted() {
		// The remedy is Git in the host vault, because the vault is Markdown in
		// a Git repository and that is the whole reason the format was chosen.
		next = "git -C " + report.HubPath + " status"
	}
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("brain.sync", brainSyncData{
			HubPath:          report.HubPath,
			HubCreated:       report.HubCreated,
			Snapshotted:      report.Snapshotted,
			ToHub:            report.ToHub,
			ToGuest:          report.ToGuest,
			ConflictOutbound: report.ConflictOutbound,
			ConflictInbound:  report.ConflictInbound,
			Notes:            notes(report.Notes),
			NextStep:         next,
		}))
	}
	state := "reconciled"
	switch {
	case report.HubCreated:
		state = "host vault created from this replica"
	case report.Conflicted():
		state = "conflict; one direction was left as it was"
	}
	lines := fmt.Sprintf("brain: %s\n"+
		"  carried to the host: %d commit(s)\n"+
		"  carried to this guest: %d commit(s)\n"+
		"  host vault: %s\n",
		state, report.ToHub, report.ToGuest, report.HubPath)
	if report.Snapshotted {
		lines += "  unsaved work in this guest's vault was committed before it was carried\n"
	}
	if _, err := fmt.Fprintf(a.stdout, "%snext: %s\n", lines, next); err != nil {
		return err
	}
	return nil
}

func (a *app) brainService(command string) (brainService, error) {
	operatorUser, err := a.lookupOperatorUser()
	if err != nil {
		return nil, &CLIError{
			Exit:    ExitExternal,
			Code:    "OPERATOR_LOOKUP_FAILED",
			Command: command,
			Message: err.Error(),
		}
	}
	adapter := a.newLima()
	return a.newBrain(adapter, lima.BootstrapOptions{OperatorUser: operatorUser, Backend: a.backend}), nil
}

type brainStatusData struct {
	State            string `json:"state"`
	Path             string `json:"path"`
	PathExists       bool   `json:"path_exists"`
	NativeFilesystem bool   `json:"native_filesystem"`
	FSType           string `json:"fstype"`
	Owner            string `json:"owner"`
	Group            string `json:"group"`
	Mode             string `json:"mode"`
	GitState         string `json:"git_state"`
	GitHasRemote     bool   `json:"git_has_remote"`
	MarkdownFiles    int    `json:"markdown_files"`
	AttachmentFiles  int    `json:"attachment_files"`
	TotalBytes       int64  `json:"total_bytes"`
	RetrievalSkill   string `json:"retrieval_skill"`
	// How far this replica stands from the one Brain on the host (ADR-0025).
	// hub_ref_known is false on a box that has never reconciled, where the two
	// counts would otherwise read as agreement.
	HubRefKnown bool     `json:"hub_ref_known"`
	AheadOfHub  int      `json:"commits_ahead_of_hub"`
	BehindHub   int      `json:"commits_behind_hub"`
	Issues      []string `json:"issues"`
}

type brainInitData struct {
	Created bool `json:"created"`
	// SkillUpdated distinguishes "the payload was written now" from "it was
	// already current", which is the only signal a caller has for deciding
	// whether running agent sessions are stale.
	SkillUpdated bool `json:"retrieval_skill_updated"`
	brainStatusData
}

type brainTransferData struct {
	DryRun         bool           `json:"dry_run"`
	Files          int            `json:"files"`
	Markdown       int            `json:"markdown_files"`
	Attachments    int            `json:"attachment_files"`
	Bytes          int64          `json:"total_bytes"`
	ManifestSHA256 string         `json:"manifest_sha256"`
	Conflicts      int            `json:"conflicts"`
	Skipped        map[string]int `json:"skipped"`
	FinalPath      string         `json:"final_path"`
}

func brainData(report brain.StatusReport) brainStatusData {
	issues := append([]string(nil), report.Issues...)
	if issues == nil {
		issues = []string{}
	}
	return brainStatusData{
		State:            string(report.State),
		Path:             report.Path,
		PathExists:       report.PathExists,
		NativeFilesystem: report.NativeFilesystem,
		FSType:           report.FSType,
		Owner:            report.Owner,
		Group:            report.Group,
		Mode:             report.Mode,
		GitState:         string(report.GitState),
		GitHasRemote:     report.GitHasRemote,
		MarkdownFiles:    report.MarkdownFiles,
		AttachmentFiles:  report.AttachmentFiles,
		TotalBytes:       report.TotalBytes,
		RetrievalSkill:   string(report.SkillState),
		HubRefKnown:      report.HubRefKnown,
		AheadOfHub:       report.AheadOfHub,
		BehindHub:        report.BehindHub,
		Issues:           issues,
	}
}

func (a *app) emitBrainInit(report brain.InitReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("brain.init", brainInitData{
			Created:         report.Created,
			SkillUpdated:    report.SkillUpdated,
			brainStatusData: brainData(report.Status),
		}))
	}
	action := "unchanged"
	if report.Created {
		action = "created"
	}
	if _, err := fmt.Fprintf(a.stdout, "Second Brain %s at %s.\n", action, report.Status.Path); err != nil {
		return err
	}
	return a.emitBrainStatus("brain.init", report.Status, brainSkillNotes(report.Status.SkillState, report.Status.SkillPath, report.SkillUpdated))
}

func transferData(report brain.TransferReport) brainTransferData {
	skipped := report.Skipped
	if skipped == nil {
		skipped = map[string]int{}
	}
	return brainTransferData{
		DryRun:         report.DryRun,
		Files:          report.Files,
		Markdown:       report.Markdown,
		Attachments:    report.Attachments,
		Bytes:          report.Bytes,
		ManifestSHA256: report.ManifestSHA256,
		Conflicts:      report.Conflicts,
		Skipped:        skipped,
		FinalPath:      report.FinalPath,
	}
}

func brainTransferDetails(report brain.TransferReport) map[string]any {
	data := transferData(report)
	return map[string]any{
		"dry_run":          data.DryRun,
		"files":            data.Files,
		"markdown_files":   data.Markdown,
		"attachment_files": data.Attachments,
		"total_bytes":      data.Bytes,
		"manifest_sha256":  data.ManifestSHA256,
		"conflicts":        data.Conflicts,
		"skipped":          data.Skipped,
		"final_path":       data.FinalPath,
	}
}

func (a *app) emitBrainTransfer(command string, report brain.TransferReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope(command, transferData(report)))
	}
	action := "completed"
	if report.DryRun {
		action = "dry-run"
	}
	_, err := fmt.Fprintf(a.stdout,
		"Brain transfer %s.\n"+
			"  final path:   %s\n"+
			"  files:        %d\n"+
			"  markdown:     %d\n"+
			"  attachments:  %d\n"+
			"  total bytes:  %d\n"+
			"  conflicts:    %d\n"+
			"  manifest:     %s\n",
		action, report.FinalPath, report.Files, report.Markdown, report.Attachments,
		report.Bytes, report.Conflicts, report.ManifestSHA256)
	return err
}

// brainSkillNotes states what Torio actually verified. It checked a file on the
// guest; it cannot inspect a running agent process, which reads its skills at
// startup and does not rebuild that when a file appears.
//
// The path comes from the report because it is the backend's path. Naming a
// fixed one here would have every box told to look under another backend's
// profile — a wrong answer printed with full confidence by the command whose
// only job is to report what is on the guest.
func brainSkillNotes(state brain.SkillState, path string, updated bool) []string {
	switch {
	case state == brain.SkillNotApplicable:
		return []string{
			"This backend discovers no skills, so there is no retrieval skill to install.",
			"The vault is still readable: it is a directory the agent can search by path.",
		}
	case state == brain.SkillInstalled && updated:
		return []string{
			"Retrieval skill written to " + path + ".",
			"Agents read their skills at startup: start a new session to use it.",
		}
	case state == brain.SkillInstalled:
		return []string{
			"Retrieval skill already current at " + path + ".",
			"Torio verified the file only; it cannot tell whether a running session has loaded it.",
		}
	case state == brain.SkillDrift:
		return []string{
			"Retrieval skill at " + path + " does not match the payload Torio ships.",
			"Run 'torio brain init' to reinstall it, then start a new session.",
		}
	default:
		return []string{
			"Retrieval skill is not installed; the Brain is not searchable from other projects.",
			"Run 'torio brain init' to install it.",
		}
	}
}

func (a *app) emitBrainStatus(command string, report brain.StatusReport, extra []string) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope(command, brainData(report)))
	}
	issues := "none"
	if len(report.Issues) > 0 {
		issues = strings.Join(report.Issues, ",")
	}
	// Where this replica stands relative to the one Brain. A box that has never
	// reconciled is told so rather than shown two zeroes, which would read as
	// being level with a hub it has never met (ADR-0025).
	hub := "never reconciled with the host vault"
	if report.HubRefKnown {
		hub = fmt.Sprintf("%d ahead, %d behind the host vault", report.AheadOfHub, report.BehindHub)
	}
	_, err := fmt.Fprintf(a.stdout,
		"Brain: %s\n"+
			"  path:        %s\n"+
			"  filesystem:  %s (native=%t)\n"+
			"  owner/mode:  %s:%s %s\n"+
			"  git:         %s (remote=%t)\n"+
			"  replica:     %s\n"+
			"  markdown:    %d files\n"+
			"  attachments: %d files\n"+
			"  total bytes: %d\n"+
			"  skill:       %s\n"+
			"  issues:      %s\n",
		report.State, report.Path, report.FSType, report.NativeFilesystem,
		report.Owner, report.Group, report.Mode, report.GitState, report.GitHasRemote,
		hub, report.MarkdownFiles, report.AttachmentFiles, report.TotalBytes,
		report.SkillState, issues)
	if err != nil {
		return err
	}
	for _, line := range extra {
		if _, err := fmt.Fprintln(a.stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func brainStatusDetails(report brain.StatusReport) map[string]any {
	if report.Path == "" && report.State == "" {
		return nil
	}
	data := brainData(report)
	return map[string]any{
		"state":             data.State,
		"path":              data.Path,
		"path_exists":       data.PathExists,
		"native_filesystem": data.NativeFilesystem,
		"fstype":            data.FSType,
		"owner":             data.Owner,
		"group":             data.Group,
		"mode":              data.Mode,
		"git_state":         data.GitState,
		"git_has_remote":    data.GitHasRemote,
		"markdown_files":    data.MarkdownFiles,
		"attachment_files":  data.AttachmentFiles,
		"total_bytes":       data.TotalBytes,
		"retrieval_skill":   data.RetrievalSkill,
		"issues":            data.Issues,
	}
}

func mapBrainError(command string, err error) *CLIError {
	var brainErr *brain.Error
	if !errors.As(err, &brainErr) {
		out := internalError(err.Error())
		out.Command = command
		return out
	}
	code := strings.ToUpper(string(brainErr.Kind))
	switch brainErr.Kind {
	case brain.KindPrecondition:
		return &CLIError{Exit: ExitPrecondition, Code: code, Command: command, Message: brainErr.Error()}
	case brain.KindConflict:
		return &CLIError{Exit: ExitConflict, Code: code, Command: command, Message: brainErr.Error()}
	case brain.KindVerification:
		return &CLIError{Exit: ExitVerification, Code: code, Command: command, Message: brainErr.Error()}
	case brain.KindGit, brain.KindRegistration, brain.KindGuestCommand,
		brain.KindTransport, brain.KindTimeout, brain.KindCancelled:
		return &CLIError{Exit: ExitExternal, Code: code, Command: command, Message: brainErr.Error()}
	default:
		out := internalError(brainErr.Error())
		out.Command = command
		return out
	}
}
