/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   plugins:
 *     - lean-ai-provenance
 *   skills:
 *     - mark-ai-provenance
 */
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/lima"
)

type brainService interface {
	Init(context.Context) (brain.InitReport, error)
	Status(context.Context) (brain.StatusReport, error)
}

func newBrainCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Initialize and inspect the private Markdown Second Brain",
		Long: "Manage the mandatory Torio V1 Second Brain at /home/hermes/brain. " +
			"The Brain stays on the guest's native filesystem, is private to hermes, " +
			"and is registered as a separate Hermes Project. Commands report only " +
			"bounded aggregate metadata, never note names or content.",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio brain --help'")
			}
			return usageError(fmt.Sprintf("unknown brain subcommand %q", args[0]))
		},
	}
	cmd.AddCommand(newBrainInitCmd(a))
	cmd.AddCommand(newBrainStatusCmd(a))
	return cmd
}

func newBrainInitCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create or verify the private Git-versioned Brain",
		Long: "Create the canonical Markdown scaffold atomically through private guest staging, " +
			"make its initial local Git commit, and register the separate Second Brain Hermes " +
			"Project. Idempotent for matching managed state; refuses non-empty unmanaged data. " +
			"Does not install the retrieval skill, configure a remote, or push.",
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
			"Hermes Project registration, and retrieval skill state. No note names or content " +
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
			return a.emitBrainStatus("brain.status", report, nil)
		},
	}
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
	return a.newBrain(adapter, lima.BootstrapOptions{OperatorUser: operatorUser}), nil
}

type brainStatusData struct {
	State             string   `json:"state"`
	Path              string   `json:"path"`
	PathExists        bool     `json:"path_exists"`
	NativeFilesystem  bool     `json:"native_filesystem"`
	FSType            string   `json:"fstype"`
	Owner             string   `json:"owner"`
	Group             string   `json:"group"`
	Mode              string   `json:"mode"`
	GitState          string   `json:"git_state"`
	GitHasRemote      bool     `json:"git_has_remote"`
	MarkdownFiles     int      `json:"markdown_files"`
	AttachmentFiles   int      `json:"attachment_files"`
	TotalBytes        int64    `json:"total_bytes"`
	ProjectRegistered bool     `json:"project_registered"`
	RetrievalSkill    string   `json:"retrieval_skill"`
	Issues            []string `json:"issues"`
}

type brainInitData struct {
	Created bool `json:"created"`
	brainStatusData
}

func brainData(report brain.StatusReport) brainStatusData {
	issues := append([]string(nil), report.Issues...)
	if issues == nil {
		issues = []string{}
	}
	return brainStatusData{
		State:             string(report.State),
		Path:              report.Path,
		PathExists:        report.PathExists,
		NativeFilesystem:  report.NativeFilesystem,
		FSType:            report.FSType,
		Owner:             report.Owner,
		Group:             report.Group,
		Mode:              report.Mode,
		GitState:          string(report.GitState),
		GitHasRemote:      report.GitHasRemote,
		MarkdownFiles:     report.MarkdownFiles,
		AttachmentFiles:   report.AttachmentFiles,
		TotalBytes:        report.TotalBytes,
		ProjectRegistered: report.ProjectRegistered,
		RetrievalSkill:    string(report.SkillState),
		Issues:            issues,
	}
}

func (a *app) emitBrainInit(report brain.InitReport) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("brain.init", brainInitData{
			Created:         report.Created,
			brainStatusData: brainData(report.Status),
		}, nil))
	}
	action := "unchanged"
	if report.Created {
		action = "created"
	}
	if _, err := fmt.Fprintf(a.stdout, "Second Brain %s at %s.\n", action, report.Status.Path); err != nil {
		return err
	}
	return a.emitBrainStatus("brain.init", report.Status, []string{"Retrieval skill remains not installed until Task 13."})
}

func (a *app) emitBrainStatus(command string, report brain.StatusReport, extra []string) error {
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope(command, brainData(report), nil))
	}
	project := "not_registered"
	if report.ProjectRegistered {
		project = "registered"
	}
	if report.ProjectConflict {
		project = "conflict"
	}
	issues := "none"
	if len(report.Issues) > 0 {
		issues = strings.Join(report.Issues, ",")
	}
	_, err := fmt.Fprintf(a.stdout,
		"Brain: %s\n"+
			"  path:        %s\n"+
			"  filesystem:  %s (native=%t)\n"+
			"  owner/mode:  %s:%s %s\n"+
			"  git:         %s (remote=%t)\n"+
			"  markdown:    %d files\n"+
			"  attachments: %d files\n"+
			"  total bytes: %d\n"+
			"  project:     %s\n"+
			"  skill:       %s\n"+
			"  issues:      %s\n",
		report.State, report.Path, report.FSType, report.NativeFilesystem,
		report.Owner, report.Group, report.Mode, report.GitState, report.GitHasRemote,
		report.MarkdownFiles, report.AttachmentFiles, report.TotalBytes,
		project, report.SkillState, issues)
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
		"state":              data.State,
		"path":               data.Path,
		"path_exists":        data.PathExists,
		"native_filesystem":  data.NativeFilesystem,
		"fstype":             data.FSType,
		"owner":              data.Owner,
		"group":              data.Group,
		"mode":               data.Mode,
		"git_state":          data.GitState,
		"git_has_remote":     data.GitHasRemote,
		"markdown_files":     data.MarkdownFiles,
		"attachment_files":   data.AttachmentFiles,
		"total_bytes":        data.TotalBytes,
		"project_registered": data.ProjectRegistered,
		"retrieval_skill":    data.RetrievalSkill,
		"issues":             data.Issues,
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
