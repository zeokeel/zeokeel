package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zeokeel/zeokeel/internal/session"
	appstate "github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// RunHeadless runs one prompt in a throwaway session without a window and
// returns the process exit code: 0 on success, 1 when the turn failed, was
// stopped or an approval was denied, and 2 for a usage error. It is the
// `zeokeel run` backend (FP-18): no GUI, no tray and no single-instance lock, so
// editor integrations and CI can drive the agent.
//
// With --json the result is one JSON object on stdout; otherwise the final
// assistant text is printed. Diagnostics always go to stderr. Tool calls that
// would need human approval are denied unless --yes is passed, because a
// non-interactive run must never block on a prompt.
func RunHeadless(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	errOut := &syncWriter{w: stderr}
	opts, err := parseHeadlessArgs(args)
	if err != nil {
		fmt.Fprintf(errOut, "zeokeel run: %v\n\n%s\n", err, headlessUsage)
		return 2
	}
	if opts.help {
		fmt.Fprint(stdout, headlessUsage)
		return 0
	}

	dir := DataDir()
	a := &app{}
	var startup *AgentBundle
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		root := workspaceRootForStore(st, dir)
		bundle, berr := buildAgentWithOptions(st, root, DefaultInstruction, a.toolOptions(st, root, ""))
		startup = bundle
		return bundle, berr
	})
	if err != nil {
		fmt.Fprintf(errOut, "zeokeel run: %v\n", err)
		return 1
	}
	defer func() {
		a.closeToolManagers()
		if a.bundle != nil && a.bundle != startup && a.bundle.Close != nil {
			_ = a.bundle.Close()
		}
		_ = cleanup()
	}()
	if startup == nil {
		fmt.Fprintln(errOut, "zeokeel run: no model is configured (set provider.api_key / the default model in Settings, or DEEPSEEK_API_KEY)")
		return 1
	}
	// Wire the same rebuild hook the desktop app uses so a workspace or access
	// mode change rebuilds the tool set for the right root instead of quietly
	// running the startup agent.
	a.services = ctrl
	a.bundle = startup
	// One-shot runs still persist /goal-created state the same way the desktop
	// app does (the goal tool already persisted; this makes the controller API
	// path durable too).
	ctrl.SetGoalStore(storeToolState{st: ctrl.Store()})
	if startup.Agent != nil {
		ctrl.SetAgentModeSwitcher(startup.Agent.SetMode)
	}
	ctrl.SetAgentRebuilder(a.rebuildAgent)
	// Record the access mode the startup agent was actually built for, exactly
	// like the desktop app does after every rebuild. Without this the controller
	// has no baseline: SetSessionPermission/rebuildForPermission treat "" as
	// "startup rebuild is in charge" and skip, so --permission would be silently
	// ignored and the run would execute under the persisted default mode
	// (found by independent verification of FP-18).
	ctrl.SetBuiltPermission(permissionMode(ctrl.Store()))

	var denied atomic.Int32
	ctrl.SetOnApprovalRequested(func(_ string, req appstate.ApprovalRequest) {
		if opts.yes {
			if !ctrl.Approve(req.CallID) {
				fmt.Fprintf(errOut, "zeokeel run: could not auto-approve tool %q\n", req.Name)
			}
			return
		}
		denied.Add(1)
		fmt.Fprintf(errOut, "zeokeel run: tool %q requires human approval; headless mode denies it (pass --yes to auto-approve)\n", req.Name)
		if !ctrl.Deny(req.CallID) {
			fmt.Fprintf(errOut, "zeokeel run: could not deny tool %q\n", req.Name)
		}
	})

	// Ctrl-C cancels the turn and lets the controller persist its terminal
	// state before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	sessionID, err := ctrl.CreateSession(ctx, headlessSessionTitle(opts.prompt))
	if err != nil {
		fmt.Fprintf(errOut, "zeokeel run: create session: %v\n", err)
		return 1
	}
	if opts.permission != "" {
		ctrl.SetSessionPermission(sessionID, opts.permission)
	}
	if err := ctrl.Submit(ctx, sessionID, opts.prompt); err != nil {
		fmt.Fprintf(errOut, "zeokeel run: submit: %v\n", err)
		return 1
	}
	// Wait for the turn and any goal continuation it scheduled. Wait only sees
	// the turns that exist when it is called, so re-check the phase and wait
	// again if another turn started in between. A failed turn ends in
	// PhaseFailed instead of PhaseIdle; both are terminal for this run.
	var snap *appstate.Snapshot
	for {
		ctrl.Wait()
		snap = ctrl.Snapshot()
		if snap.Phase == appstate.PhaseIdle || snap.Phase == appstate.PhaseFailed {
			break
		}
	}
	text := headlessFinalText(snap)
	status := "ok"
	if snap.TurnReason != appstate.ReasonNone {
		status = string(snap.TurnReason)
	} else if denied.Load() > 0 {
		// The turn can still "complete" after a denied tool call; the run did
		// not do what was asked, so it must not report success.
		status = "denied"
	}
	exitCode := 0
	if status != "ok" {
		exitCode = 1
	}
	errText := ""
	if snap.LastError != nil {
		errText = snap.LastError.Text
	}
	if status != "ok" && errText != "" {
		fmt.Fprintf(errOut, "zeokeel run: turn %s: %s\n", status, errText)
	}

	if opts.json {
		result := headlessResult{
			Status:    status,
			SessionID: sessionID,
			Text:      text,
			Error:     errText,
			Denied:    int(denied.Load()),
			Usage: headlessUsageCounters{
				Turns:    snap.Usage.Turns,
				Steps:    snap.Usage.Steps,
				In:       snap.Usage.In,
				Out:      snap.Usage.Out,
				CacheHit: snap.Usage.CacheHit,
			},
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(errOut, "zeokeel run: encode result: %v\n", err)
			return 1
		}
	} else if text != "" {
		fmt.Fprintln(stdout, text)
	}
	return exitCode
}

// headlessUsage is the `zeokeel run` help text. The cmd package also prints it
// for `run --help`; keeping it here means the backend and the CLI cannot drift.
const headlessUsage = `Usage: zeokeel run [options] [prompt]

Run one prompt non-interactively (no window, no single-instance lock) and
print the final assistant answer. The prompt may be positional or --prompt.

Options:
  -p, --prompt <text>   Prompt text (positional arguments are joined as well).
      --json            Print one JSON object: status, session_id, text,
                        error, denied, usage.
      --yes             Auto-approve tool calls that would ask the user.
                        Without it such calls are denied, so the run can never
                        hang waiting for a human.
      --permission <m>  Session access mode: read-only | workspace |
                        auto-review | full. Defaults to the app setting.
  -h, --help            Show this help.

Exit codes: 0 success, 1 the turn failed/was stopped/an approval was denied,
2 usage error.`

// headlessOptions is the parsed `zeokeel run` command line.
type headlessOptions struct {
	prompt     string
	json       bool
	yes        bool
	permission string
	help       bool
}

// headlessResult is the --json document.
type headlessResult struct {
	Status    string                `json:"status"`
	SessionID string                `json:"session_id,omitempty"`
	Text      string                `json:"text"`
	Error     string                `json:"error,omitempty"`
	Denied    int                   `json:"denied,omitempty"`
	Usage     headlessUsageCounters `json:"usage"`
}

// headlessUsageCounters mirrors the snapshot's cumulative counters.
type headlessUsageCounters struct {
	Turns    int `json:"turns"`
	Steps    int `json:"steps"`
	In       int `json:"in"`
	Out      int `json:"out"`
	CacheHit int `json:"cache_hit"`
}

// parseHeadlessArgs parses the `zeokeel run` arguments. Unknown flags and a
// missing prompt are usage errors (exit 2); -- and -h are supported.
func parseHeadlessArgs(args []string) (headlessOptions, error) {
	var opts headlessOptions
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			opts.json = true
		case arg == "--yes" || arg == "-y":
			opts.yes = true
		case arg == "--help" || arg == "-h":
			opts.help = true
		case arg == "--prompt" || arg == "-p":
			i++
			if i >= len(args) {
				return opts, errors.New("--prompt requires a value")
			}
			opts.prompt = args[i]
		case strings.HasPrefix(arg, "--prompt="):
			opts.prompt = strings.TrimPrefix(arg, "--prompt=")
		case strings.HasPrefix(arg, "--permission="):
			opts.permission = strings.TrimPrefix(arg, "--permission=")
		case arg == "--permission":
			i++
			if i >= len(args) {
				return opts, errors.New("--permission requires a value")
			}
			opts.permission = args[i]
		case arg == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(arg, "-") && arg != "-":
			return opts, fmt.Errorf("unknown flag %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if opts.help {
		return opts, nil
	}
	if len(positional) > 0 {
		if opts.prompt != "" {
			return opts, errors.New("prompt given both positionally and with --prompt")
		}
		opts.prompt = strings.Join(positional, " ")
	}
	opts.prompt = strings.TrimSpace(opts.prompt)
	if opts.prompt == "" {
		return opts, errors.New("missing prompt")
	}
	if opts.permission != "" {
		mode, err := headlessPermission(opts.permission)
		if err != nil {
			return opts, err
		}
		opts.permission = mode
	}
	return opts, nil
}

// headlessPermission maps the CLI spelling to a session access mode.
func headlessPermission(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read-only", "readonly", "ask", "read":
		return session.PermissionAsk, nil
	case "workspace", "workspace-write", "write":
		return session.PermissionWorkspace, nil
	case "auto-review", "auto_review", "review":
		return session.PermissionAutoReview, nil
	case "full", "danger-full-access", "yolo":
		return session.PermissionFull, nil
	default:
		return "", fmt.Errorf("unknown permission %q (want read-only, workspace, auto-review or full)", value)
	}
}

// headlessSessionTitle derives a session title from the prompt.
func headlessSessionTitle(prompt string) string {
	line := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if line == "" {
		return "Headless run"
	}
	const maxRunes = 60
	runes := []rune(line)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return line
}

// headlessFinalText returns the last visible assistant message, which is the
// model's final answer for the turn.
func headlessFinalText(snap *appstate.Snapshot) string {
	if snap == nil {
		return ""
	}
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		msg := snap.Messages[i]
		if msg.Role == appstate.RoleAssistant && strings.TrimSpace(msg.Text) != "" {
			return msg.Text
		}
	}
	return ""
}

// syncWriter serializes writes from the turn goroutine (approval notices) and
// the main goroutine (result/errors), so an arbitrary io.Writer such as a test
// buffer cannot be written concurrently.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
