package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/projects"
)

// projectService is the manager surface the project commands drive. It is an
// interface for the same reason brainService is: the command layer owns argument
// shape, output and exit-code mapping, and must be testable without a guest.
type projectService interface {
	Add(context.Context, projects.AddRequest) (projects.AddReport, error)
	List() ([]projects.Project, error)
	Show(context.Context, string) (projects.ShowReport, error)
	Use(context.Context, string) (projects.UseReport, error)
	Remove(context.Context, string) (projects.RemoveReport, error)
	EnterPreflight(context.Context, string) (projects.EnterSession, error)
	ShellPreflight(context.Context, string) (projects.ShellSession, error)
	CheckServiceEnv(context.Context) (projects.ServiceEnvCheck, error)
}

var _ projectService = (*projects.Manager)(nil)

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Attach and manage Git projects on the Torio VM",
		Long: "Attach repositories to the managed guest, inspect them, and forget them. " +
			"The workspace path is always derived as " + lima.HermesWorkspacePath + "/<id>, " +
			"never taken from an operator. Torio stores no Git credentials: a remote the " +
			"guest cannot already read noninteractively fails closed.",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError("no subcommand given; run 'torio project --help'")
			}
			return usageError(fmt.Sprintf("unknown project subcommand %q", args[0]))
		},
	}
	cmd.AddCommand(newProjectAddCmd(a))
	cmd.AddCommand(newProjectListCmd(a))
	cmd.AddCommand(newProjectShowCmd(a))
	cmd.AddCommand(newProjectUseCmd(a))
	cmd.AddCommand(newProjectRemoveCmd(a))
	cmd.AddCommand(newProjectEnterCmd(a))
	cmd.AddCommand(newProjectShellCmd(a))
	return cmd
}

func newProjectAddCmd(a *app) *cobra.Command {
	var id string
	var use bool
	cmd := &cobra.Command{
		Use:   "add <name> <remote>",
		Short: "Clone or adopt a repository and register it with Hermes",
		Long: "Clone the exact remote into the derived workspace path, or verify and adopt a " +
			"checkout that is already there, give the operator and hermes shared access, and " +
			"register the project with Hermes before recording it in config. Nothing on the " +
			"guest is reset, cleaned or deleted, so a rerun after a failure finishes the work.\n\n" +
			"Without --id the project id is <name> itself, which must be a lowercase slug " +
			"(letters, digits, inner hyphens); pass --id to choose one explicitly.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.projectService("project.add")
			if err != nil {
				return err
			}
			name, remote := args[0], args[1]
			projectID := id
			if projectID == "" {
				projectID = name
			}
			report, err := service.Add(ctx, projects.AddRequest{
				ID:          projectID,
				DisplayName: name,
				Remote:      remote,
				Use:         use,
			})
			if err != nil {
				cliErr := mapProjectError("project.add", err)
				cliErr.Details = projectNotesDetails(report.Notes)
				return cliErr
			}
			return a.emitProjectAdd(report)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "project id (slug) to use instead of <name>")
	cmd.Flags().BoolVar(&use, "use", false, "make the project active in Hermes after a successful add")
	return cmd
}

func newProjectListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the registered projects",
		Long: "List the project registry and the workspace path each id derives. It reads " +
			"config only and runs no guest command, so it works with the VM down.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := a.projectService("project.list")
			if err != nil {
				return err
			}
			list, err := service.List()
			if err != nil {
				return mapProjectError("project.list", err)
			}
			return a.emitProjectList(list)
		},
	}
}

func newProjectShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Report the state of one attached project",
		Long: "Report the registry entry, the state of the guest checkout and of the Hermes " +
			"registration. It reports drift as stable markers instead of repairing it, and " +
			"never returns file names, diffs or raw Git output.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.projectService("project.show")
			if err != nil {
				return err
			}
			report, err := service.Show(ctx, args[0])
			if err != nil {
				return mapProjectError("project.show", err)
			}
			return a.emitProjectShow(report)
		},
	}
}

func newProjectUseCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "use <id>",
		Short: "Make a registered project the active Hermes project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.projectService("project.use")
			if err != nil {
				return err
			}
			report, err := service.Use(ctx, args[0])
			if err != nil {
				return mapProjectError("project.use", err)
			}
			return a.emitProjectUse(report)
		},
	}
}

func newProjectRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Forget a project without touching its checkout",
		Long: "Archive the Hermes project and remove the config entry. The guest checkout is " +
			"never deleted and the output says where it still is; there is no --delete.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.projectService("project.remove")
			if err != nil {
				return err
			}
			report, err := service.Remove(ctx, args[0])
			if err != nil {
				cliErr := mapProjectError("project.remove", err)
				cliErr.Details = projectNotesDetails(report.Notes)
				return cliErr
			}
			return a.emitProjectRemove(report)
		},
	}
}

// newProjectEnterCmd opens an ordinary interactive project session. The SSH
// transport disables agent forwarding; project shell remains the explicit
// push-capable boundary.
func newProjectEnterCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "enter <id>",
		Short: "Open a project terminal without Git remote write capability",
		Long: "Open an interactive terminal in the project checkout without forwarding the " +
			"operator's SSH agent. The session can edit and commit locally, but it does not " +
			"receive Git remote write capability. Use `torio project shell <id>` only when an " +
			"operator intentionally needs to push. This command is interactive and does not " +
			"support --json.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return &CLIError{
					Exit:    ExitUsage,
					Code:    "USAGE",
					Command: "project.enter",
					Message: "project enter is interactive and has no machine output; drop --json",
				}
			}
			service, err := a.projectService("project.enter")
			if err != nil {
				return err
			}
			ctx, cancel := a.opContext(cmd)
			session, err := service.EnterPreflight(ctx, args[0])
			cancel()
			if err != nil {
				return mapProjectError("project.enter", err)
			}
			enterCmd, err := a.newEnterSpec(session.Project.Path)
			if err != nil {
				return mapLimaError("project.enter", err)
			}
			if _, err := fmt.Fprintf(a.stdout,
				"%s: opening a project session in %s without SSH agent forwarding\n"+
					"  Git remote write capability is not enabled; use `torio project shell %s` only when you intend to push\n",
				session.Project.ID, session.Project.Path, session.Project.ID); err != nil {
				return err
			}
			runErr := a.newInteractive().RunInteractive(cmd.Context(), enterCmd)
			if _, err := fmt.Fprintf(a.stdout, "%s: project session ended\n", session.Project.ID); err != nil {
				return err
			}
			if runErr != nil {
				return mapInteractiveSessionError("project.enter", "project session", runErr)
			}
			return nil
		},
	}
}

// newProjectShellCmd opens the ephemeral operator session. It is the one project
// command with no machine output: it hands the operator's terminal to a remote
// shell, so there is no document to emit and --json is a usage error rather than
// a silently ignored flag.
func newProjectShellCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "shell <id>",
		Short: "Open an ephemeral operator shell in a project checkout",
		Long: "Open an interactive session in the project checkout with the operator's SSH " +
			"agent forwarded, which is the only way write capability reaches the guest. The " +
			"capability lives until you exit.\n\n" +
			"The session is preflighted first: the project must be registered, the VM " +
			"bootstrap-verified, the checkout present with the registered origin and shared " +
			"permissions, and the local SSH agent must hold an identity to forward. Torio " +
			"never test-pushes to prove any of it. This command is interactive: it does not " +
			"support --json.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return &CLIError{
					Exit:    ExitUsage,
					Code:    "USAGE",
					Command: "project.shell",
					Message: "project shell is interactive and has no machine output; drop --json",
				}
			}
			service, err := a.projectService("project.shell")
			if err != nil {
				return err
			}
			session, err := a.preflightShell(cmd, service, args[0])
			if err != nil {
				return err
			}
			shellCmd, err := a.newShellSpec(session.Project.Path)
			if err != nil {
				return mapLimaError("project.shell", err)
			}
			if err := a.announceShell(session); err != nil {
				return err
			}
			// Deliberately the command context, not a.opContext: an operator session
			// ends when the operator ends it. Bounding it with the operation timeout
			// would kill a live shell mid-push.
			runErr := a.newInteractive().RunInteractive(cmd.Context(), shellCmd)
			return a.reportShellEnd(cmd, service, session, runErr)
		},
	}
}

// preflightShell runs the preflight under the bounded operation context. Only
// the session itself is unbounded: the checks that decide whether to open it are
// ordinary guest commands and must not hang.
func (a *app) preflightShell(cmd *cobra.Command, service projectService, id string) (projects.ShellSession, error) {
	ctx, cancel := a.opContext(cmd)
	defer cancel()
	session, err := service.ShellPreflight(ctx, id)
	if err != nil {
		return projects.ShellSession{}, mapProjectError("project.shell", err)
	}
	return session, nil
}

// announceShell states what the operator is about to hold and for how long,
// from the host side. The guest helper prints its own line once the session is
// up; this one is printed even when the transport never gets there.
func (a *app) announceShell(session projects.ShellSession) error {
	_, err := fmt.Fprintf(a.stdout,
		"%s: opening an operator session in %s\n"+
			"  your SSH agent is forwarded for this session only; the write capability ends at exit\n",
		session.Project.ID, session.Project.Path)
	return err
}

// reportShellEnd closes the session out. It reports one fact — the session
// ended — and disclaims the one an operator might otherwise read into it: Torio
// never sees the remote side, so it cannot and does not say what was pushed.
//
// The post-session service environment check runs on both paths. A session that
// ended at exit 130 forwarded exactly the same agent as one that ended cleanly,
// so the invariant is worth just as much. A detected leak outranks the child's
// own exit status: the session is over either way, and a forwarded agent that
// outlived it is the more serious finding.
func (a *app) reportShellEnd(cmd *cobra.Command, service projectService, session projects.ShellSession, runErr error) error {
	if _, err := fmt.Fprintf(a.stdout,
		"%s: operator session ended\n"+
			"  torio makes no claim about what was pushed; check the remote yourself\n",
		session.Project.ID); err != nil {
		return err
	}

	ctx, cancel := a.opContext(cmd)
	defer cancel()
	check, checkErr := service.CheckServiceEnv(ctx)
	if _, err := fmt.Fprintf(a.stdout, "  hermes service environment: %s\n", serviceEnvState(check)); err != nil {
		return err
	}
	if checkErr != nil {
		return mapProjectError("project.shell", checkErr)
	}
	if runErr != nil {
		return mapOperatorShellError(runErr)
	}
	return nil
}

// projectService builds the manager for one command run. The operator identity
// is the second trusted identity of every shared checkout, so a run that cannot
// resolve it fails before any guest work.
func (a *app) projectService(command string) (projectService, error) {
	operatorUser, err := a.lookupOperatorUser()
	if err != nil {
		return nil, &CLIError{
			Exit:    ExitExternal,
			Code:    "OPERATOR_LOOKUP_FAILED",
			Command: command,
			Message: err.Error(),
		}
	}
	return a.newProjects(a.newLima(), lima.BootstrapOptions{OperatorUser: operatorUser}), nil
}

// projectData is the registry identity of one project. The remote is safe to
// echo: the config layer refuses to store one carrying a credential.
type projectData struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Remote      string `json:"remote"`
	Path        string `json:"path"`
}

type projectAddData struct {
	projectData
	Cloned         bool     `json:"cloned"`
	Adopted        bool     `json:"adopted"`
	HermesCreated  bool     `json:"hermes_created"`
	HermesRestored bool     `json:"hermes_restored"`
	Registered     bool     `json:"registered"`
	Activated      bool     `json:"activated"`
	Notes          []string `json:"notes"`
	NextStep       string   `json:"next_step"`
}

type projectListData struct {
	Projects []projectData `json:"projects"`
	Count    int           `json:"count"`
}

type projectShowData struct {
	projectData
	Checkout projectCheckoutData `json:"checkout"`
	Hermes   projectHermesData   `json:"hermes"`
	Issues   []string            `json:"issues"`
	NextStep string              `json:"next_step"`
}

type projectCheckoutData struct {
	PathExists         bool   `json:"path_exists"`
	Symlink            bool   `json:"symlink"`
	Directory          bool   `json:"directory"`
	Repository         bool   `json:"repository"`
	OriginMatches      bool   `json:"origin_matches"`
	FullClone          bool   `json:"full_clone"`
	Clean              bool   `json:"clean"`
	NoCredentialHelper bool   `json:"no_credential_helper"`
	SharedPermissions  bool   `json:"shared_permissions"`
	Owner              string `json:"owner"`
	Group              string `json:"group"`
	Mode               string `json:"mode"`
}

type projectHermesData struct {
	Present        bool `json:"present"`
	Archived       bool `json:"archived"`
	PrimaryMatches bool `json:"primary_matches"`
	Registered     bool `json:"registered"`
}

type projectUseData struct {
	projectData
	Active   bool   `json:"active"`
	NextStep string `json:"next_step"`
}

type projectRemoveData struct {
	projectData
	HermesArchived        bool     `json:"hermes_archived"`
	HermesAlreadyArchived bool     `json:"hermes_already_archived"`
	HermesAbsent          bool     `json:"hermes_absent"`
	CheckoutRetained      bool     `json:"checkout_retained"`
	CheckoutPath          string   `json:"checkout_path"`
	Notes                 []string `json:"notes"`
	NextStep              string   `json:"next_step"`
}

func projectView(p projects.Project) projectData {
	return projectData{ID: p.ID, DisplayName: p.DisplayName, Remote: p.Remote, Path: p.Path}
}

func (a *app) emitProjectAdd(report projects.AddReport) error {
	next := "torio project use " + report.Project.ID
	if report.Activated {
		next = "torio project enter " + report.Project.ID
	}
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("project.add", projectAddData{
			projectData:    projectView(report.Project),
			Cloned:         report.Cloned,
			Adopted:        report.Adopted,
			HermesCreated:  report.HermesCreated,
			HermesRestored: report.HermesRestored,
			Registered:     report.Registered,
			Activated:      report.Activated,
			Notes:          notes(report.Notes),
			NextStep:       next,
		}))
	}
	state := "attached"
	switch {
	case report.Cloned:
		state = "cloned"
	case report.Adopted:
		state = "adopted"
	}
	if report.Activated {
		state += ", active"
	}
	if _, err := fmt.Fprintf(a.stdout,
		"%s: %s\n"+
			"  path:   %s\n"+
			"  remote: %s\n",
		report.Project.ID, state, report.Project.Path, report.Project.Remote); err != nil {
		return err
	}
	if err := a.printNotes(report.Notes); err != nil {
		return err
	}
	_, err := fmt.Fprintf(a.stdout, "next: %s\n", next)
	return err
}

func (a *app) emitProjectList(list []projects.Project) error {
	if a.jsonOut {
		out := make([]projectData, 0, len(list))
		for _, p := range list {
			out = append(out, projectView(p))
		}
		return writeJSON(a.stdout, successEnvelope("project.list",
			projectListData{Projects: out, Count: len(out)}))
	}
	if len(list) == 0 {
		_, err := fmt.Fprint(a.stdout,
			"no projects registered\n"+
				"next: torio project add <name> <remote>\n")
		return err
	}
	for _, p := range list {
		if _, err := fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", p.ID, p.DisplayName, p.Path); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(a.stdout, "next: torio project show <id>\n")
	return err
}

func (a *app) emitProjectShow(report projects.ShowReport) error {
	next := showNextStep(report)
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("project.show", projectShowData{
			projectData: projectView(report.Project),
			Checkout:    checkoutView(report.Checkout),
			Hermes:      hermesView(report.Hermes),
			Issues:      notes(report.Issues),
			NextStep:    next,
		}))
	}
	state := "ok"
	if len(report.Issues) > 0 {
		state = "drift"
	}
	issues := "none"
	if len(report.Issues) > 0 {
		issues = strings.Join(report.Issues, ",")
	}
	_, err := fmt.Fprintf(a.stdout,
		"%s: %s\n"+
			"  path:      %s\n"+
			"  remote:    %s\n"+
			"  checkout:  present=%t repository=%t origin=%t clean=%t shared=%t\n"+
			"  ownership: %s:%s %s\n"+
			"  hermes:    %s\n"+
			"  issues:    %s\n"+
			"next: %s\n",
		report.Project.ID, state, report.Project.Path, report.Project.Remote,
		report.Checkout.PathExists, report.Checkout.Repository, report.Checkout.OriginMatches,
		report.Checkout.Clean, report.Checkout.SharedPermissions,
		report.Checkout.Owner, report.Checkout.Group, report.Checkout.Mode,
		hermesState(report.Hermes), issues, next)
	return err
}

func (a *app) emitProjectUse(report projects.UseReport) error {
	next := "torio project enter " + report.Project.ID
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("project.use", projectUseData{
			projectData: projectView(report.Project),
			Active:      true,
			NextStep:    next,
		}))
	}
	_, err := fmt.Fprintf(a.stdout, "%s: active\nnext: %s\n", report.Project.ID, next)
	return err
}

func (a *app) emitProjectRemove(report projects.RemoveReport) error {
	const next = "torio project list"
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("project.remove", projectRemoveData{
			projectData:           projectView(report.Project),
			HermesArchived:        report.HermesArchived,
			HermesAlreadyArchived: report.HermesAlreadyArchived,
			HermesAbsent:          report.HermesAbsent,
			CheckoutRetained:      report.CheckoutRetained,
			CheckoutPath:          report.CheckoutPath,
			Notes:                 notes(report.Notes),
			NextStep:              next,
		}))
	}
	hermes := "hermes project archived"
	switch {
	case report.HermesAlreadyArchived:
		hermes = "hermes project already archived"
	case report.HermesAbsent:
		hermes = "hermes project absent"
	}
	// The retained checkout is stated, not implied: `remove` forgets a project,
	// and an operator who reads "removed" must not have to guess whether their
	// working tree is still on the guest.
	if _, err := fmt.Fprintf(a.stdout,
		"%s: removed from the registry (%s)\n"+
			"  the checkout directory %s still exists on the VM; Torio never deletes it\n"+
			"next: %s\n",
		report.Project.ID, hermes, report.CheckoutPath, next); err != nil {
		return err
	}
	return nil
}

// showNextStep picks the one command that actually moves the reported state
// forward. `show` never repairs anything, so the choice is between the two ways
// drift gets resolved: a rerun of `add`, which reclones a missing checkout and
// reconciles the Hermes registration, and an operator session, for drift inside
// a checkout that exists — Torio will not reset, clean or repoint a working
// tree, so only a human can.
func showNextStep(report projects.ShowReport) string {
	if len(report.Issues) == 0 {
		return "torio project enter " + report.Project.ID
	}
	for _, issue := range report.Issues {
		if strings.HasPrefix(issue, "hermes_") || issue == "checkout_absent" {
			continue
		}
		return "torio project enter " + report.Project.ID
	}
	return fmt.Sprintf("torio project add %q %s --id %s",
		report.Project.DisplayName, report.Project.Remote, report.Project.ID)
}

func checkoutView(c projects.CheckoutStatus) projectCheckoutData {
	return projectCheckoutData{
		PathExists:         c.PathExists,
		Symlink:            c.Symlink,
		Directory:          c.Directory,
		Repository:         c.Repository,
		OriginMatches:      c.OriginMatches,
		FullClone:          c.FullClone,
		Clean:              c.Clean,
		NoCredentialHelper: c.NoCredentialHelper,
		SharedPermissions:  c.SharedPermissions,
		Owner:              c.Owner,
		Group:              c.Group,
		Mode:               c.Mode,
	}
}

func hermesView(h projects.HermesStatus) projectHermesData {
	return projectHermesData{
		Present:        h.Present,
		Archived:       h.Archived,
		PrimaryMatches: h.PrimaryMatches,
		Registered:     h.Present && !h.Archived && h.PrimaryMatches,
	}
}

func hermesState(h projects.HermesStatus) string {
	switch {
	case !h.Present:
		return "absent"
	case !h.PrimaryMatches:
		return "conflict"
	case h.Archived:
		return "archived"
	default:
		return "registered"
	}
}

// notes normalizes a marker slice so the envelope always carries an array.
func notes(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func (a *app) printNotes(in []string) error {
	if len(in) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(a.stdout, "  notes:  %s\n", strings.Join(in, ","))
	return err
}

// projectNotesDetails carries the state markers a failed operation left behind,
// so a failing command still tells the operator what a rerun will finish.
func projectNotesDetails(in []string) map[string]any {
	if len(in) == 0 {
		return nil
	}
	return map[string]any{"notes": strings.Join(in, ",")}
}

// serviceEnvState names the three outcomes of the post-session check. A guest
// with no backend installed reports "not checked" rather than "clean": there
// was nothing to leak into, which is not the same as having looked and found
// nothing.
func serviceEnvState(check projects.ServiceEnvCheck) string {
	switch {
	case check.AgentSocketPresent:
		return "SSH_AUTH_SOCK present"
	case check.Checked:
		return "no forwarded agent socket"
	default:
		return "not checked"
	}
}

// mapOperatorShellError classifies the end of an interactive session. A remote
// shell exiting non-zero is the same external-command-failure class as any other
// non-zero exit under a Torio adapter (see sshCommandFailed): the child's code
// travels in the message, never as torio's own exit code, so the contract table
// stays closed.
func mapOperatorShellError(err error) *CLIError {
	return mapInteractiveSessionError("project.shell", "operator session", err)
}

func mapInteractiveSessionError(command, label string, err error) *CLIError {
	var exitErr *execx.ExitError
	if errors.As(err, &exitErr) {
		return &CLIError{
			Exit:    ExitExternal,
			Code:    "COMMAND_FAILED",
			Command: command,
			Message: fmt.Sprintf("%s exited %d", label, exitErr.Code),
		}
	}
	return &CLIError{
		Exit:    ExitExternal,
		Code:    "COMMAND_FAILED",
		Command: command,
		Message: err.Error(),
	}
}

// mapProjectError maps a *projects.Error onto the CLI exit-code contract via
// ErrorKind (never string matching).
//
// Two kinds are worth naming. KindAuth is a permission denial (exit 7), not an
// external outage: the guest reached the remote and was refused, and the remedy
// is a human provisioning access out of band — Torio stores no credentials and
// retrying changes nothing. KindConfigWrite is reconciliation required (exit 9):
// the guest work succeeded and only the registry write did not, so the guest and
// config now disagree and a rerun finishes the operation.
func mapProjectError(command string, err error) *CLIError {
	var perr *projects.Error
	if !errors.As(err, &perr) {
		out := internalError(err.Error())
		out.Command = command
		return out
	}
	code := strings.ToUpper(string(perr.Kind))
	switch perr.Kind {
	case projects.KindInvalidConfig:
		return &CLIError{Exit: ExitUsage, Code: code, Command: command, Message: perr.Error()}
	case projects.KindPrecondition:
		return &CLIError{Exit: ExitPrecondition, Code: code, Command: command, Message: perr.Error()}
	case projects.KindAuth:
		return &CLIError{Exit: ExitPermission, Code: code, Command: command, Message: perr.Error()}
	case projects.KindConflict:
		return &CLIError{Exit: ExitConflict, Code: code, Command: command, Message: perr.Error()}
	case projects.KindVerification:
		return &CLIError{Exit: ExitVerification, Code: code, Command: command, Message: perr.Error()}
	case projects.KindConfigWrite:
		return &CLIError{Exit: ExitReconcile, Code: code, Command: command, Message: perr.Error()}
	case projects.KindGuestCommand, projects.KindGit, projects.KindRegistration,
		projects.KindTransport, projects.KindTimeout, projects.KindCancelled:
		return &CLIError{Exit: ExitExternal, Code: code, Command: command, Message: perr.Error()}
	default:
		out := internalError(perr.Error())
		out.Command = command
		return out
	}
}
