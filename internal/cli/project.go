package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	RemoteAccess(context.Context, string, projects.SessionIdentity) (projects.RemoteAccess, error)
	CheckServiceEnv(context.Context) (projects.ServiceEnvCheck, error)
}

var _ projectService = (*projects.Manager)(nil)

func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Attach and manage Git projects on the Torio VM",
		Long: "Attach repositories to the managed guest, inspect them, and forget them. " +
			"The workspace path is always derived as <backend workspace>/<id>, never taken " +
			"from an operator, so it moves with --backend and a project can exist in more " +
			"than one guest without either checkout being addressable from the other. " +
			"Torio stores no Git credentials: a remote the guest cannot already read " +
			"noninteractively fails closed.",
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
	cmd.AddCommand(newProjectAgentCmd(a))
	return cmd
}

func newProjectAddCmd(a *app) *cobra.Command {
	var id string
	var use bool
	cmd := &cobra.Command{
		Use:   "add <name> [remote]",
		Short: "Clone or adopt a repository and attach it to the selected backend's guest",
		Long: "Clone the exact remote into the derived workspace path, or verify and adopt a " +
			"checkout that is already there, give the operator and the backend identity shared " +
			"access, register the project if the backend keeps a registry, and only then record " +
			"it. Nothing on the guest is reset, cleaned or deleted, so a rerun after a failure " +
			"finishes the work.\n\n" +
			"The registry is shared by every instance, the checkouts are not: a project exists " +
			"once, in one guest per backend that has materialized it. Rerun with the id alone " +
			"and no remote — `torio project add demo --backend claude-code` — to materialize an " +
			"already registered project in another backend's guest, using the remote already on " +
			"record. That is a separate step rather than something an interactive command does " +
			"for you, because cloning reaches a Git remote.\n\n" +
			"Without --id the project id is <name> itself, which must be a lowercase slug " +
			"(letters, digits, inner hyphens); pass --id to choose one explicitly.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			service, err := a.projectService("project.add")
			if err != nil {
				return err
			}
			name := args[0]
			projectID := id
			if projectID == "" {
				projectID = name
			}
			var remote string
			if len(args) == 2 {
				remote = args[1]
			} else {
				known, err := findRegistered(service, projectID)
				if err != nil {
					return err
				}
				remote, name = known.Remote, known.DisplayName
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
	cmd.Flags().BoolVar(&use, "use", false, "make the project active in the backend's registry after a successful add")
	return cmd
}

// findRegistered resolves an already-registered project by id, for the one-
// argument form of `project add`.
//
// It reads the shared registry rather than accepting a remote the operator
// retyped, so materializing a project in a second backend's guest cannot
// silently attach a different repository under a name that already means
// something. An unregistered id is a usage error naming the missing argument:
// there is nothing on record to complete it from.
func findRegistered(service projectService, id string) (projects.Project, error) {
	list, err := service.List()
	if err != nil {
		return projects.Project{}, mapProjectError("project.add", err)
	}
	for _, p := range list {
		if p.ID == id {
			return p, nil
		}
	}
	return projects.Project{}, &CLIError{
		Exit:    ExitUsage,
		Code:    "USAGE",
		Command: "project.add",
		Message: fmt.Sprintf("project %q is not registered, so there is no remote on record; pass one: torio project add %s <remote>", id, id),
	}
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
// newProjectAgentCmd opens the backend's own session in a checkout. It
// completes the triad: `enter` is you without push capability, `shell` is you
// with it, `agent` is the agent, which never has it.
//
// It carries no machine output for the same reason `enter` does not: it hands
// the operator's terminal to a remote process, so there is no document to emit.
func newProjectAgentCmd(a *app) *cobra.Command {
	var pushGrant bool
	cmd := &cobra.Command{
		Use:   "agent <id>",
		Short: "Open the backend's own session in a project checkout",
		Long: "Start the configured backend inside the project checkout, running as the " +
			"backend's guest identity rather than as you.\n\n" +
			"No SSH agent is forwarded and the connection is never multiplexed, so the " +
			"session cannot reach a Git remote and cannot inherit a connection that " +
			"can. The agent edits and commits in a tree it owns; pushing stays yours, " +
			"from `torio project shell <id>`, after you have read what it did.\n\n" +
			"Inside the box the backend runs without permission prompts. That is not a " +
			"weakening: the prompt was a control inside the agent's own process, and " +
			"the box replaced it with ones the agent cannot reach — an unprivileged " +
			"identity, a closed group set, no route to a remote, and the edge of the " +
			"VM.\n\n" +
			"A backend that declares no interactive session has nothing to open here. " +
			"This command is interactive and does not support --json.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return &CLIError{
					Exit:    ExitUsage,
					Code:    "USAGE",
					Command: "project.agent",
					Message: "project agent is interactive and has no machine output; drop --json",
				}
			}
			if a.backend.Session() == nil {
				return &CLIError{
					Exit:    ExitPrecondition,
					Code:    "BACKEND_NO_SESSION",
					Command: "project.agent",
					Message: fmt.Sprintf("backend %q declares no interactive session; its surface is the guest service it runs (torio serve status)", a.backend.Identity().Name),
				}
			}
			service, err := a.projectService("project.agent")
			if err != nil {
				return err
			}
			ctx, cancel := a.opContext(cmd)
			session, err := service.EnterPreflight(ctx, args[0])
			cancel()
			if err != nil {
				return mapProjectError("project.agent", err)
			}
			if !pushGrant {
				agentCmd, err := a.newAgentSpec(session.Project.Path)
				if err != nil {
					return mapLimaError("project.agent", err)
				}
				if _, err := fmt.Fprintf(a.stdout,
					"%s: starting %s in %s as the backend identity\n"+
						"  No SSH agent is forwarded. The agent can commit here; pushing is yours, from `torio project shell %s`\n",
					session.Project.ID, a.backend.Identity().Name, session.Project.Path, session.Project.ID); err != nil {
					return err
				}
				runErr := a.newInteractive().RunInteractive(cmd.Context(), agentCmd)
				if _, err := fmt.Fprintf(a.stdout, "%s: agent session ended. Review what it did before you push.\n", session.Project.ID); err != nil {
					return err
				}
				if runErr != nil {
					return mapInteractiveSessionError("project.agent", "agent session", runErr)
				}
				return nil
			}

			// A grant with no pinned key would be the old rejected design: a
			// socket handed to the agent with nothing in front of it. The whole
			// reason this is offerable is the dialog, so no pin is a refusal
			// rather than a weaker grant.
			if a.runtime.File.OperatorKey == "" {
				return &CLIError{
					Exit:    ExitPrecondition,
					Code:    "PRECONDITION_FAILED",
					Command: "project.agent",
					Message: "--push-grant needs `operator_key` set in the config document: the grant is the mediated agent, and without a pinned key there is nothing to mediate",
				}
			}
			if err := a.requireReachableRemote(cmd, service, args[0]); err != nil {
				return err
			}
			mediated, err := a.startMediation("project.agent", mediatedContext(session.Project, session.Review))
			if err != nil {
				return err
			}
			defer mediated.stop()

			guestSocket, err := lima.NewGuestPushSocketPath()
			if err != nil {
				return mapLimaError("project.agent", err)
			}
			agentCmd, err := a.newAgentPushSpec(session.Project.Path, mediated.SocketPath(), guestSocket)
			if err != nil {
				return mapLimaError("project.agent", err)
			}
			if _, err := fmt.Fprintf(a.stdout,
				"%s: starting %s in %s as the backend identity\n"+
					"  This session may ask to push. One pinned key is reachable through Torio; every signature\n"+
					"  stops at a dialog on this Mac and is recorded in %s\n",
				session.Project.ID, a.backend.Identity().Name, session.Project.Path,
				filepath.Join(a.runtime.Paths.ConfigDir, agentAuditFileName)); err != nil {
				return err
			}
			if line := reviewLine(session.Review); line != "" {
				if _, err := fmt.Fprintf(a.stdout, "  %s\n", line); err != nil {
					return err
				}
			}
			runErr := a.newInteractive().RunInteractive(cmd.Context(), agentCmd)
			if _, err := fmt.Fprintf(a.stdout,
				"%s: agent session ended. Torio makes no claim about what was pushed; the decision log says what it allowed.\n",
				session.Project.ID); err != nil {
				return err
			}
			if runErr != nil {
				return mapInteractiveSessionError("project.agent", "agent session", runErr)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&pushGrant, "push-grant", false,
		"Let this session ask to push, through the mediated agent. Every signature waits for your confirmation on this Mac and is recorded. Requires operator_key.")
	return cmd
}

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
			// The proxy is started before the argv is built, because the argv
			// names its socket. It is stopped on every exit path below, so the
			// capability cannot outlive the session that carried it.
			mediated, err := a.startMediation("project.shell", mediatedContext(session.Project, session.Review))
			if err != nil {
				return err
			}
			defer mediated.stop()

			var shellCmd execx.InteractiveCommand
			if socket := mediated.SocketPath(); socket != "" {
				shellCmd, err = a.newMediatedShellSpec(session.Project.Path, socket)
			} else {
				shellCmd, err = a.newShellSpec(session.Project.Path)
			}
			if err != nil {
				return mapLimaError("project.shell", err)
			}
			if err := a.announceShell(session, mediated != nil); err != nil {
				return err
			}
			if err := a.noteRemoteAccess(cmd, service, "project.shell", args[0], projects.OperatorIdentity); err != nil {
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
//
// The two forwarding shapes are named differently on purpose. They look
// identical from inside the session and only one of them can reach every key the
// operator has loaded, so the line that says which is the operator's only
// warning that a config change did not take.
func (a *app) announceShell(session projects.ShellSession, mediated bool) error {
	carrier := "  your SSH agent is forwarded for this session only; the write capability ends at exit\n"
	if mediated {
		carrier = "  one pinned key is forwarded through Torio for this session; every signature asks you first\n"
	}
	if _, err := fmt.Fprintf(a.stdout,
		"%s: opening an operator session in %s\n%s",
		session.Project.ID, session.Project.Path, carrier); err != nil {
		return err
	}
	if line := reviewLine(session.Review); line != "" {
		if _, err := fmt.Fprintf(a.stdout, "  %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

// reviewLine describes the checkout as the preflight found it, so the first two
// commands of every session do not have to be `git status` and `git diff`.
//
// It is a snapshot and is worded as one. An empty result is the honest answer
// for a detached HEAD or a branch with no upstream: there is no count to give,
// and inventing a zero would read as "nothing to push".
func reviewLine(review projects.ReviewContext) string {
	if review.Branch == "" {
		return ""
	}
	if !review.AheadKnown {
		return fmt.Sprintf("on %s, with no upstream configured to compare against", review.Branch)
	}
	switch review.Ahead {
	case 0:
		return fmt.Sprintf("on %s, level with its upstream right now", review.Branch)
	case 1:
		return fmt.Sprintf("on %s, 1 commit ahead of its upstream right now", review.Branch)
	default:
		return fmt.Sprintf("on %s, %d commits ahead of its upstream right now", review.Branch, review.Ahead)
	}
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
	// The backend travels with the options, exactly as it does for `vm`, `brain`
	// and `backend`. Without it the project manager falls back to the backend
	// Torio shipped first, so on any other instance every `project` command
	// verified the wrong identity's bootstrap, resolved the wrong workspace, and
	// asked for a session the wrong backend declares.
	return a.newProjects(a.newLima(), lima.BootstrapOptions{OperatorUser: operatorUser, Backend: a.backend}), nil
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

// projectShowData mirrors `serve status`: the declaration comes first, because
// it decides whether the block after it means anything. On a backend that keeps
// no registry the `hermes` object is absent rather than all-false — an object
// full of falses reads as "the registration is missing", which is a different
// statement from "there is nowhere to register".
type projectShowData struct {
	projectData
	Checkout         projectCheckoutData `json:"checkout"`
	RegistryDeclared bool                `json:"registry_declared"`
	Hermes           *projectHermesData  `json:"hermes,omitempty"`
	Issues           []string            `json:"issues"`
	NextStep         string              `json:"next_step"`
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

// projectRemoveData keeps the three hermes_* flags flat and always present, so
// the envelope a Hermes instance emits is byte-for-byte what it was. On a
// backend that keeps no registry all three are false, and registry_declared is
// what says why: there was nothing to archive, rather than an archival that
// failed to happen.
type projectRemoveData struct {
	projectData
	RegistryDeclared      bool     `json:"registry_declared"`
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
	// `use` selects the active project in the backend's registry, so on a
	// backend that declares none it is a command that fails closed by design.
	// Naming it as the next step would send the operator to a `NO_REGISTRY`
	// error to learn something Torio already knows here.
	next := "torio project use " + report.Project.ID
	if report.Activated || a.backend.Registry() == nil {
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
	declared := a.backend.Registry() != nil
	if a.jsonOut {
		return writeJSON(a.stdout, successEnvelope("project.show", projectShowData{
			projectData:      projectView(report.Project),
			Checkout:         checkoutView(report.Checkout),
			RegistryDeclared: declared,
			Hermes:           hermesView(declared, report.Hermes),
			Issues:           notes(report.Issues),
			NextStep:         next,
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
	// The label follows the declaration, not just its value. `hermes:` on a box
	// that runs a different agent names a backend that is not there, whatever
	// the value beside it says.
	registration := fmt.Sprintf("  hermes:    %s\n", hermesState(report.Hermes))
	if !declared {
		registration = fmt.Sprintf("  registry:  none declared by backend %q\n", a.backend.Identity().Name)
	}
	_, err := fmt.Fprintf(a.stdout,
		"%s: %s\n"+
			"  path:      %s\n"+
			"  remote:    %s\n"+
			"  checkout:  present=%t repository=%t origin=%t clean=%t shared=%t\n"+
			"  ownership: %s:%s %s\n"+
			"%s"+
			"  issues:    %s\n"+
			"next: %s\n",
		report.Project.ID, state, report.Project.Path, report.Project.Remote,
		report.Checkout.PathExists, report.Checkout.Repository, report.Checkout.OriginMatches,
		report.Checkout.Clean, report.Checkout.SharedPermissions,
		report.Checkout.Owner, report.Checkout.Group, report.Checkout.Mode,
		registration, issues, next)
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
			RegistryDeclared:      a.backend.Registry() != nil,
			HermesArchived:        report.HermesArchived,
			HermesAlreadyArchived: report.HermesAlreadyArchived,
			HermesAbsent:          report.HermesAbsent,
			CheckoutRetained:      report.CheckoutRetained,
			CheckoutPath:          report.CheckoutPath,
			Notes:                 notes(report.Notes),
			NextStep:              next,
		}))
	}
	// The default branch claims an archival happened, so a backend that keeps no
	// registry needs its own: nothing was archived there, and saying otherwise
	// asserts an action Torio did not take.
	hermes := "hermes project archived"
	switch {
	case a.backend.Registry() == nil:
		hermes = "no registry to archive it from"
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

// hermesView returns the registration block, or nil when the backend declares
// no registry. Nil is what keeps the block out of the envelope entirely: there
// is no registration to describe, so describing one as missing would report a
// shape as a fault.
func hermesView(declared bool, h projects.HermesStatus) *projectHermesData {
	if !declared {
		return nil
	}
	return &projectHermesData{
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
	case projects.KindPrecondition, projects.KindNoRegistry:
		// no_registry maps where serve's no_service maps: managing a capability
		// the backend never declared is an unmet precondition, not a guest that
		// failed.
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

// remoteAccess resolves how a session would reach the origin, under the bounded
// operation context. It is two ordinary read-only guest commands and must not
// hang the way the session it precedes is allowed to.
func (a *app) remoteAccess(cmd *cobra.Command, service projectService, command, id string, who projects.SessionIdentity) (projects.RemoteAccess, error) {
	ctx, cancel := a.opContext(cmd)
	defer cancel()
	access, err := service.RemoteAccess(ctx, id, who)
	if err != nil {
		return projects.RemoteAccess{}, mapProjectError(command, err)
	}
	return access, nil
}

// noteRemoteAccess tells the operator what the session they just opened will not
// be able to do.
//
// It is a note rather than a refusal: an operator opens a shell to read, commit
// and fix things, and only sometimes to push. Refusing the session because the
// push at the end of it would fail would be answering a question nobody asked.
func (a *app) noteRemoteAccess(cmd *cobra.Command, service projectService, command, id string, who projects.SessionIdentity) error {
	access, err := a.remoteAccess(cmd, service, command, id, who)
	if err != nil {
		return err
	}
	switch {
	case access.Transport != projects.TransportSSH:
		_, err = fmt.Fprintf(a.stdout,
			"  note: origin pushes over %s, which never uses an SSH agent; nothing here will ask you to approve a signature\n",
			access.Transport)
	case access.Host == "":
		_, err = fmt.Fprintf(a.stdout,
			"  note: origin's push host could not be read as a plain hostname, so Torio cannot say whether its key is trusted\n")
	case !access.HostKnown:
		_, err = fmt.Fprintf(a.stdout,
			"  note: %s is not in this identity's known_hosts; a push will stop at host key verification, not at anything to do with your key\n",
			access.Host)
	}
	return err
}

// requireReachableRemote refuses a grant the session could not use.
//
// The grant exists for one purpose. A session opened to push, against a remote
// no push can reach, is a session whose whole point fails at the end — after the
// agent has done the work, and with an error about host keys that reads like a
// problem with the key the operator just pinned.
func (a *app) requireReachableRemote(cmd *cobra.Command, service projectService, id string) error {
	access, err := a.remoteAccess(cmd, service, "project.agent", id, projects.AgentIdentity)
	if err != nil {
		return err
	}
	refuse := func(msg string) error {
		return &CLIError{Exit: ExitPrecondition, Code: "PRECONDITION_FAILED", Command: "project.agent", Message: msg}
	}
	switch {
	case access.Transport != projects.TransportSSH:
		return refuse(fmt.Sprintf(
			"origin pushes over %s, which never uses an SSH agent: the grant would hand this session a key nothing can use. Point origin's push URL at SSH, or push it yourself.",
			access.Transport))
	case access.Host == "":
		return refuse("origin's push host could not be read as a plain hostname, so Torio cannot prove its key is trusted before granting a session that would use it")
	case !access.HostKnown:
		return refuse(fmt.Sprintf(
			"%s is not in the agent identity's known_hosts, so a push would stop before reaching your key. Add the host key from a source you trust rather than from whatever answers on port 22.",
			access.Host))
	}
	return nil
}
