package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/tools/winenc"
	"github.com/zeokeel/zeokeel/old/internal/winproc"
)

// Persistent terminal tools.
//
// A terminal is a long-lived login shell with a combined output ring buffer.
// On Unix the shell runs under a real pseudoterminal: reads come from the PTY
// master, writes go to it, and the child is a session leader with the PTY as
// its controlling terminal, so it echoes input and supports line editing and
// job control. If a PTY cannot be allocated (restricted sandboxes, unsupported
// platforms), the session transparently falls back to the pipe implementation:
// stdin is a pipe, stdout/stderr are joined into the ring, and the surfaced
// "pty" flag is false. Non-Unix platforms (terminal_other.go) always use pipes.
//
// Each shell runs in its own process group/session, so terminal_signal can
// address the foreground job and terminal_close can reap the whole tree. The
// shell is started as a login shell ("sh -l") rather than "sh -lc <command>"
// because a -c process exits after its one command and cannot keep cwd/env
// state across tool calls.

const (
	// DefaultTerminalMaxBytes is the default retained scrollback per terminal.
	DefaultTerminalMaxBytes = 256 << 10

	// DefaultTerminalIdleTimeout is the default idle window: a send waits at
	// most this long for output to go quiet, and a terminal with no activity for
	// this long is closed and reaped.
	DefaultTerminalIdleTimeout = 30 * time.Second

	// DefaultTerminalMax is the maximum number of concurrent terminal sessions.
	DefaultTerminalMax = 4

	terminalQuietPeriod  = 150 * time.Millisecond
	terminalCloseTimeout = 3 * time.Second
	terminalPollPeriod   = 20 * time.Millisecond
	terminalMinReapEvery = 50 * time.Millisecond
	terminalMaxReapEvery = 5 * time.Second
)

// TerminalConfig configures the persistent terminal tools.
type TerminalConfig struct {
	// WorkspaceRoot is the directory terminals start in and the only root that
	// terminal_open cwd values may resolve inside. Required.
	WorkspaceRoot string

	// Shell and ShellArgs override the platform default (a login "sh" on Unix,
	// cmd.exe on non-Unix systems). When Shell is set and ShellArgs is nil no
	// arguments are added.
	Shell     string
	ShellArgs []string

	// MaxOutputBytes is the retained scrollback cap per terminal. Non-positive
	// values use DefaultTerminalMaxBytes.
	MaxOutputBytes int

	// MaxTerminals caps concurrent sessions. Non-positive values use
	// DefaultTerminalMax.
	MaxTerminals int

	// IdleTimeout bounds how long terminal_send waits for quiet output and how
	// long an untouched session stays alive. Non-positive values use
	// DefaultTerminalIdleTimeout.
	IdleTimeout time.Duration
}

// terminalShellDefaults returns the platform default shell and its arguments.
// It is a variable so terminal_other.go can replace it on non-Unix systems.
var terminalShellDefaults = func() (string, []string) {
	return "sh", []string{"-l"}
}

// terminalDeliverSignal sends sig to a membership group. The default follows
// POSIX process-group addressing (a negative pid); terminal_other.go replaces
// it where that convention does not exist.
var terminalDeliverSignal = func(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid process group %d", pgid)
	}
	p, err := os.FindProcess(-pgid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

// TerminalManager owns the persistent terminal sessions behind the six
// terminal tools. The app keeps one across agent rebuilds so an id the model
// learned in an earlier turn keeps working; NewTerminalTools remains the
// single-registry convenience constructor.
type TerminalManager struct{ m *terminalManager }

// NewTerminalManager builds a manager for cfg. Like NewTerminalTools it never
// fails: a bad configuration is reported by each tool's Invoke call.
func NewTerminalManager(cfg TerminalConfig) *TerminalManager {
	return &TerminalManager{m: newTerminalManager(cfg)}
}

// Tools returns the six terminal tools bound to this manager.
func (tm *TerminalManager) Tools() []runtime.Tool {
	if tm == nil || tm.m == nil {
		return nil
	}
	m := tm.m
	return []runtime.Tool{
		&terminalOpenTool{mgr: m},
		&terminalListTool{mgr: m},
		&terminalReadTool{mgr: m},
		&terminalSendTool{mgr: m},
		&terminalSignalTool{mgr: m},
		&terminalCloseTool{mgr: m},
	}
}

// Close terminates every retained terminal.
func (tm *TerminalManager) Close() error {
	if tm == nil || tm.m == nil {
		return nil
	}
	var first error
	for _, s := range tm.m.list() {
		if _, err := tm.m.close(s); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// NewTerminalTools constructs the six persistent terminal tools. All tools
// share one manager. The constructor never fails: an invalid configuration is
// reported by each tool's Invoke call.
func NewTerminalTools(cfg TerminalConfig) []runtime.Tool {
	return NewTerminalManager(cfg).Tools()
}

// outputRing is a thread-safe byte ring that retains the newest limit bytes
// written. It is shared by the terminal and background-job managers.
type outputRing struct {
	mu      sync.Mutex
	buf     []byte
	limit   int
	total   int64
	dropped int64
}

func newOutputRing(limit int) *outputRing {
	if limit < 0 {
		limit = 0
	}
	return &outputRing{limit: limit}
}

func (r *outputRing) Write(p []byte) (int, error) {
	n := len(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total += int64(n)
	if r.limit == 0 {
		r.dropped += int64(n)
		return n, nil
	}
	if n >= r.limit {
		r.dropped += int64(len(r.buf)) + int64(n-r.limit)
		r.buf = append(r.buf[:0], p[n-r.limit:]...)
		return n, nil
	}
	if overflow := len(r.buf) + n - r.limit; overflow > 0 {
		copy(r.buf, r.buf[overflow:])
		r.buf = r.buf[:len(r.buf)-overflow]
		r.dropped += int64(overflow)
	}
	r.buf = append(r.buf, p...)
	return n, nil
}

// snapshot copies the retained bytes from from onward. from is clamped to the
// retained window; start is the retained window's first byte, end the total
// byte count.
func (r *outputRing) snapshot(from int64) (data []byte, start, end, total int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start = r.total - int64(len(r.buf))
	end = r.total
	total = r.total
	if from < start {
		from = start
	}
	if from > end {
		from = end
	}
	return append([]byte(nil), r.buf[from-start:]...), start, end, total
}

func (r *outputRing) totalBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

func (r *outputRing) droppedBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

type terminalManager struct {
	mu  sync.Mutex
	sb  *sandbox
	cfg TerminalConfig

	sessions map[string]*terminalSession
	nextID   int
	root     string
	outLimit int
	max      int
	idle     time.Duration
	shell    string
	args     []string

	reaperRunning bool
	reapEvery     time.Duration
	initErr       error
}

func newTerminalManager(cfg TerminalConfig) *terminalManager {
	m := &terminalManager{
		cfg:      cfg,
		sessions: make(map[string]*terminalSession),
		outLimit: cfg.MaxOutputBytes,
		max:      cfg.MaxTerminals,
		idle:     cfg.IdleTimeout,
	}
	if m.outLimit <= 0 {
		m.outLimit = DefaultTerminalMaxBytes
	}
	if m.max <= 0 {
		m.max = DefaultTerminalMax
	}
	if m.idle <= 0 {
		m.idle = DefaultTerminalIdleTimeout
	}
	m.reapEvery = m.idle / 4
	if m.reapEvery < terminalMinReapEvery {
		m.reapEvery = terminalMinReapEvery
	}
	if m.reapEvery > terminalMaxReapEvery {
		m.reapEvery = terminalMaxReapEvery
	}

	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		m.initErr = fmt.Errorf("terminal: WorkspaceRoot is required")
		return m
	}
	sb, err := newSandbox(cfg.WorkspaceRoot)
	if err != nil {
		m.initErr = fmt.Errorf("terminal: %w", err)
		return m
	}
	m.sb = sb
	m.root = sb.root

	if cfg.Shell != "" {
		m.shell = cfg.Shell
		m.args = append([]string(nil), cfg.ShellArgs...)
	} else {
		m.shell, m.args = terminalShellDefaults()
	}
	return m
}

func (m *terminalManager) open(cwd string) (*terminalSession, error) {
	m.mu.Lock()
	if m.initErr != nil {
		err := m.initErr
		m.mu.Unlock()
		return nil, err
	}

	// Exited sessions no longer deserve a slot.
	var stale []*terminalSession
	for id, s := range m.sessions {
		if s.hasExited() {
			delete(m.sessions, id)
			stale = append(stale, s)
		}
	}
	if len(m.sessions) >= m.max {
		m.mu.Unlock()
		for _, s := range stale {
			s.closeProcess()
		}
		return nil, fmt.Errorf("terminal: maximum of %d terminals reached; close one with terminal_close", m.max)
	}

	m.nextID++
	id := fmt.Sprintf("term-%d", m.nextID)

	// The PTY attempt and the pipe fallback each need a pristine exec.Cmd; a
	// command that already failed to start must not be reused.
	newCmd := func() *exec.Cmd {
		cmd := exec.Command(m.shell, m.args...)
		cmd.Dir = cwd
		cmd.Env = setEnv(scrubEnv(os.Environ()), "PWD", cwd)
		winproc.Hide(cmd)
		cmd.WaitDelay = 2 * time.Second
		return cmd
	}
	out := newOutputRing(m.outLimit)
	s := &terminalSession{
		mgr:     m,
		id:      id,
		shell:   m.shell,
		args:    append([]string(nil), m.args...),
		cwd:     cwd,
		out:     out,
		done:    make(chan struct{}),
		started: time.Now(),
	}
	s.lastActive = s.started

	cmd, stdin, master, usePTY, err := startTerminalProcess(newCmd, out, s.touch)
	if err != nil {
		m.mu.Unlock()
		for _, st := range stale {
			st.closeProcess()
		}
		return nil, fmt.Errorf("terminal: start %q: %w", m.shell, err)
	}
	s.cmd = cmd
	s.stdin = stdin
	s.master = master
	s.usePTY = usePTY

	m.sessions[id] = s
	m.ensureReaperLocked()
	m.mu.Unlock()
	for _, st := range stale {
		st.closeProcess()
	}

	if usePTY {
		go s.readPTY()
	}
	go s.wait()
	return s, nil
}

// ensureReaperLocked starts the idle reaper if it is not already running. The
// reaper stops by itself once the manager has no sessions left.
func (m *terminalManager) ensureReaperLocked() {
	if m.reaperRunning || len(m.sessions) == 0 {
		return
	}
	m.reaperRunning = true
	go m.reaperLoop()
}

func (m *terminalManager) reaperLoop() {
	ticker := time.NewTicker(m.reapEvery)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		if len(m.sessions) == 0 {
			m.reaperRunning = false
			m.mu.Unlock()
			return
		}
		var stale []*terminalSession
		for id, s := range m.sessions {
			if s.idleExpired(m.idle) {
				delete(m.sessions, id)
				stale = append(stale, s)
			}
		}
		m.mu.Unlock()
		for _, s := range stale {
			s.closeProcess()
		}
	}
}

func (m *terminalManager) get(id string) (*terminalSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initErr != nil {
		return nil, m.initErr
	}
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("terminal: session %q not found", id)
	}
	return s, nil
}

func (m *terminalManager) list() []*terminalSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*terminalSession, 0, len(m.sessions))
	for i := 1; i <= m.nextID; i++ {
		if s, ok := m.sessions[fmt.Sprintf("term-%d", i)]; ok {
			out = append(out, s)
		}
	}
	return out
}

func (m *terminalManager) close(s *terminalSession) (bool, error) {
	m.mu.Lock()
	_, ok := m.sessions[s.id]
	if ok {
		delete(m.sessions, s.id)
	}
	m.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, s.closeProcess()
}

// remove discards a session that already exited without killing anything.
func (m *terminalManager) remove(s *terminalSession) {
	m.mu.Lock()
	delete(m.sessions, s.id)
	m.mu.Unlock()
}

type terminalSession struct {
	mgr *terminalManager

	id    string
	shell string
	args  []string
	cwd   string

	cmd *exec.Cmd
	// stdin is the child's stdin pipe on the fallback path.
	stdin io.WriteCloser
	// master is the PTY master when the session runs under a pseudoterminal;
	// it is nil on the pipe fallback.
	master *os.File
	usePTY bool
	out    *outputRing
	done   chan struct{}

	// decMu guards the incremental output decoder. Deltas from consecutive
	// sends are contiguous byte ranges, so a character split across the
	// boundary is carried into the next send instead of becoming U+FFFD.
	decMu  sync.Mutex
	dec    *winenc.StreamDecoder
	decEnd int64

	mu         sync.Mutex
	started    time.Time
	lastActive time.Time
	busy       int
	exited     bool
	exitCode   *int
	exitSignal string
	closing    bool
	closed     bool
}

type terminalActivityWriter struct {
	w     io.Writer
	touch func()
}

func (w terminalActivityWriter) Write(p []byte) (int, error) {
	w.touch()
	return w.w.Write(p)
}

// startTerminalPipes starts cmd with a stdin pipe and a combined
// stdout/stderr ring writer. It is the fallback used when a PTY cannot be
// allocated and the primary implementation on platforms without PTY support.
// The caller owns the returned stdin and closes it to hang up the shell.
func startTerminalPipes(newCmd func() *exec.Cmd, out *outputRing, touch func()) (*exec.Cmd, io.WriteCloser, error) {
	cmd := newCmd()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	cmd.Stdout = terminalActivityWriter{w: out, touch: touch}
	cmd.Stderr = terminalActivityWriter{w: out, touch: touch}
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	return cmd, stdin, nil
}

func (s *terminalSession) touch() {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

func (s *terminalSession) hasExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

func (s *terminalSession) idleExpired(idle time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy == 0 && !s.closed && time.Since(s.lastActive) >= idle
}

func (s *terminalSession) wait() {
	err := s.cmd.Wait()
	code := 0
	sig := ""
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
			if code < 0 {
				code = -1
			}
		} else {
			code = -1
			sig = err.Error()
		}
	}
	s.mu.Lock()
	s.exited = true
	s.exitCode = &code
	s.exitSignal = sig
	s.lastActive = time.Now()
	s.mu.Unlock()
	close(s.done)
}

func (s *terminalSession) setBusy(delta int) {
	s.mu.Lock()
	s.busy += delta
	s.lastActive = time.Now()
	s.mu.Unlock()
}

func (s *terminalSession) pid() int {
	if s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// signalTargets returns the process groups terminal_signal should address.
// Under a PTY with job control the foreground job can be in its own process
// group, so both that group and the shell's group are signalled: the former
// interrupts the running command (like Ctrl-C) and the latter preserves the
// historical behavior of addressing the shell itself. For pipe sessions the
// two are the same.
func (s *terminalSession) signalTargets() []int {
	shell := s.pid()
	if shell <= 0 {
		return nil
	}
	if fg := terminalForegroundPGID(s); fg > 0 && fg != shell {
		return []int{fg, shell}
	}
	return []int{shell}
}

// ptyHandle returns the PTY master, or nil for a pipe-backed session.
func (s *terminalSession) ptyHandle() *os.File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.master
}

// readPTY copies the PTY master into the scrollback ring until the master is
// closed or the slave side is gone. The kernel returns EIO (or ErrClosed after
// terminal_close) once every slave descriptor is closed, which ends the
// goroutine; it never outlives the session.
func (s *terminalSession) readPTY() {
	buf := make([]byte, 32<<10)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.touch()
			_, _ = s.out.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *terminalSession) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *terminalSession) statusLocked() string {
	if !s.exited {
		return fmt.Sprintf("running pid=%d", s.pid())
	}
	if s.exitSignal != "" {
		return "exited error=" + s.exitSignal
	}
	if s.exitCode != nil {
		return fmt.Sprintf("exited code=%d", *s.exitCode)
	}
	return "exited"
}

func (s *terminalSession) closeProcess() error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.closing = true
	exited := s.exited
	master := s.master
	stdin := s.stdin
	s.mu.Unlock()

	// A foreground job under an interactive shell may live in its own process
	// group, so remember it before closing the master; otherwise killing only
	// the shell's group could leave the job behind.
	fg := 0
	if master != nil {
		fg = terminalForegroundPGID(s)
	}

	if master != nil {
		_ = master.Close()
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	pid := s.pid()
	killAll := func() {
		if pid > 0 && !exited {
			_ = killProcessGroup(pid)
		}
		if fg > 0 && fg != pid {
			_ = killProcessGroup(fg)
		}
	}
	killAll()
	select {
	case <-s.done:
	case <-time.After(terminalCloseTimeout):
		killAll()
	}

	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *terminalSession) writeInput(input string) error {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return fmt.Errorf("terminal: session %q has exited", s.id)
	}
	if s.closing {
		s.mu.Unlock()
		return fmt.Errorf("terminal: session %q is closing", s.id)
	}
	w := io.Writer(s.stdin)
	if s.master != nil {
		w = s.master
	}
	s.mu.Unlock()
	if w == nil {
		return fmt.Errorf("terminal: session %q has no input handle", s.id)
	}

	payload := input
	if !strings.HasSuffix(payload, "\n") {
		payload += "\n"
	}
	// Do not hold the session lock across the write: a full PTY input buffer
	// or a concurrent close must not deadlock terminal_send against
	// terminal_close. os.File is safe for concurrent Write/Close.
	if _, err := io.WriteString(w, payload); err != nil {
		return fmt.Errorf("terminal: write to session %q: %w", s.id, err)
	}
	s.touch()
	return nil
}

func (s *terminalSession) send(start int64, ctx context.Context) (string, error) {
	s.setBusy(1)
	defer s.setBusy(-1)
	timer := time.NewTimer(s.mgr.idle)
	defer timer.Stop()
	ticker := time.NewTicker(terminalPollPeriod)
	defer ticker.Stop()

	lastTotal := s.out.totalBytes()
	lastChange := time.Now()
	for {
		select {
		case <-ctx.Done():
			return s.renderDelta(start, "canceled"), fmt.Errorf("terminal_send: canceled while waiting for output: %w", ctx.Err())
		case <-s.done:
			return s.renderDelta(start, "exited"), nil
		case <-timer.C:
			return s.renderDelta(start, "timeout"), nil
		case <-ticker.C:
			total := s.out.totalBytes()
			if total != lastTotal {
				lastTotal = total
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) >= terminalQuietPeriod {
				return s.renderDelta(start, "idle"), nil
			}
		}
	}
}

// decodeDelta decodes the send window [start,end) and carries a trailing split
// character into the next contiguous send. When the window does not continue
// where the previous one ended (output was skipped or the retained ring moved),
// the decoder restarts, which is the same "replacement character" behavior the
// byte offsets already had.
func (s *terminalSession) decodeDelta(data []byte, start, end int64) string {
	s.decMu.Lock()
	defer s.decMu.Unlock()
	if s.dec == nil || start != s.decEnd {
		s.dec = &winenc.StreamDecoder{}
		s.decEnd = start
	}
	text := s.dec.Decode(data)
	s.decEnd = end
	return text
}

func (s *terminalSession) renderDelta(start int64, wait string) string {
	data, retainedStart, end, total := s.out.snapshot(start)
	var b strings.Builder
	if start > 0 && start < retainedStart {
		fmt.Fprintf(&b, "[output truncated: earliest retained output starts at byte %d]\n", retainedStart)
	}
	body := s.decodeDelta(data, start, end)
	if body == "" {
		body = "(no new output)"
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "[session: %s]\n[wait: %s]\n[bytes: %d-%d of %d]", s.status(), wait, start, end, total)
	if wait == "timeout" {
		fmt.Fprintf(&b, "\n[note: output did not go quiet within %s; the session is still running. Use terminal_read to collect more output.]", s.mgr.idle)
	}
	return b.String()
}

// terminalOpenDescriptionFor describes terminal_open for goos. Unix runs under
// a PTY with a pipe fallback, so the description can promise pty=true. Windows
// (terminal_other.go) always uses stdio pipes and the platform shell, so it
// must describe pty=false and never a login "sh".
func terminalOpenDescriptionFor(goos string) string {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return `Create a persistent terminal session backed by a long-lived shell.

On Windows the session runs without a PTY (pty=false in the output) over stdio pipes with the platform shell: PowerShell 7 ("pwsh") when it is installed on PATH, otherwise %COMSPEC% (usually cmd.exe). Typed input is interpreted by that shell, so use its syntax and native Windows paths. Each session keeps its working directory and environment across calls. A terminal is confined to the workspace root. Close sessions with terminal_close when they are no longer needed.

Args JSON schema:
{"type":"object","properties":{"cwd":{"type":"string","description":"Optional workspace-relative initial working directory. Defaults to the workspace root."}},"additionalProperties":false}`
	}
	return `Create a persistent terminal session backed by a long-lived shell.

On Unix the shell runs under a real pseudoterminal (pty=true in the output), so typed input is echoed and line editing and job control work. If the environment cannot allocate a PTY, the session falls back to stdio pipes (pty=false) without failing. Each session keeps its working directory and environment across calls. A terminal is confined to the workspace root. The shell is a login shell ("sh -l" on Unix) rather than "sh -lc <command>", because a -c process exits after one command and cannot persist state. Close sessions with terminal_close when they are no longer needed.

Args JSON schema:
{"type":"object","properties":{"cwd":{"type":"string","description":"Optional workspace-relative initial working directory. Defaults to the workspace root."}},"additionalProperties":false}`
}

type terminalOpenTool struct{ mgr *terminalManager }

func (t *terminalOpenTool) Name() string { return "terminal_open" }
func (t *terminalOpenTool) Description() string {
	return terminalOpenDescriptionFor(goruntime.GOOS)
}

func (t *terminalOpenTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Cwd string `json:"cwd"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if t.mgr.sb == nil {
		if t.mgr.initErr != nil {
			return "", t.mgr.initErr
		}
		return "", fmt.Errorf("terminal_open: terminal manager is not initialized")
	}
	cwd := t.mgr.root
	if strings.TrimSpace(a.Cwd) != "" {
		resolved, err := t.mgr.sb.resolve(a.Cwd)
		if err != nil {
			return "", fmt.Errorf("terminal_open: cwd: %w", err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("terminal_open: cwd %q: %w", a.Cwd, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("terminal_open: cwd %q is not a directory", a.Cwd)
		}
		cwd = resolved
	}
	s, err := t.mgr.open(cwd)
	if err != nil {
		return "", err
	}
	argsText := strings.TrimSpace(s.shell + " " + strings.Join(s.args, " "))
	return fmt.Sprintf("started terminal session %s [%s] pid=%d pty=%t cwd=%s", s.id, argsText, s.pid(), s.usePTY, t.mgr.sb.relative(cwd)), nil
}

const terminalListDescription = `List persistent terminal sessions owned by this tool set, newest ids last, with their shell, status, PTY mode and working directory.

Args JSON schema:
{"type":"object","properties":{},"additionalProperties":false}`

type terminalListTool struct{ mgr *terminalManager }

func (t *terminalListTool) Name() string        { return "terminal_list" }
func (t *terminalListTool) Description() string { return terminalListDescription }

func (t *terminalListTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	if err := decodeArgs(t.Name(), args, &struct{}{}); err != nil {
		return "", err
	}
	if t.mgr.initErr != nil {
		return "", t.mgr.initErr
	}
	sessions := t.mgr.list()
	if len(sessions) == 0 {
		return "(no terminal sessions)", nil
	}
	var b strings.Builder
	for i, s := range sessions {
		if i > 0 {
			b.WriteByte('\n')
		}
		cwd := s.cwd
		if t.mgr.sb != nil {
			cwd = t.mgr.sb.relative(cwd)
		}
		shell := strings.TrimSpace(s.shell + " " + strings.Join(s.args, " "))
		fmt.Fprintf(&b, "%s [%s] %s pty=%t cwd=%s", s.id, shell, s.status(), s.usePTY, cwd)
	}
	return b.String(), nil
}

const terminalReadDescription = `Read retained output from a persistent terminal without sending input.

Output is retained in a 256 KiB (configurable) ring; older bytes are dropped. offset is a byte offset into the retained window and defaults to 0, the oldest retained byte. The result ends with the byte range that was returned.

Args JSON schema:
{"type":"object","properties":{"id":{"type":"string","description":"Terminal session id returned by terminal_open or terminal_list."},"offset":{"type":"integer","description":"Optional byte offset into the retained output; 0 means the oldest retained byte.","minimum":0}},"required":["id"],"additionalProperties":false}`

type terminalReadTool struct{ mgr *terminalManager }

func (t *terminalReadTool) Name() string        { return "terminal_read" }
func (t *terminalReadTool) Description() string { return terminalReadDescription }

func (t *terminalReadTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Offset int64  `json:"offset"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("terminal_read: id is required")
	}
	if a.Offset < 0 {
		return "", fmt.Errorf("terminal_read: offset must not be negative")
	}
	s, err := t.mgr.get(a.ID)
	if err != nil {
		return "", err
	}
	s.touch()
	data, start, end, total := s.out.snapshot(a.Offset)
	var b strings.Builder
	if a.Offset > 0 && a.Offset < start {
		fmt.Fprintf(&b, "[output truncated: earliest retained output starts at byte %d]\n", start)
	}
	if len(data) == 0 {
		b.WriteString("(no retained output)")
	} else {
		// terminal_read keeps its byte-offset API; a window that starts inside a
		// multi-byte character yields a replacement rune, as before.
		text := winenc.DecodeProcessOutput(data)
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "[bytes: %d-%d of %d; session: %s]", start, end, total, s.status())
	return b.String(), nil
}

const terminalSendDescription = `Send text to a persistent terminal, appending Enter when the input does not already end with a newline.

Under a PTY the input is echoed by the terminal and line editing is handled by the shell. The call waits until output has been quiet for a short interval, the session exits, or the idle timeout expires; it returns the output produced since the send began. Because there is no prompt detection, a command that produces no output immediately is reported as idle as soon as that quiet interval passes. On timeout the session keeps running; use terminal_read to collect later output.

Args JSON schema:
{"type":"object","properties":{"id":{"type":"string","description":"Terminal session id returned by terminal_open or terminal_list."},"input":{"type":"string","description":"UTF-8 text to write to the terminal's input."}},"required":["id","input"],"additionalProperties":false}`

type terminalSendTool struct{ mgr *terminalManager }

func (t *terminalSendTool) Name() string        { return "terminal_send" }
func (t *terminalSendTool) Description() string { return terminalSendDescription }

func (t *terminalSendTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID    string `json:"id"`
		Input string `json:"input"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("terminal_send: id is required")
	}
	s, err := t.mgr.get(a.ID)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	start := s.out.totalBytes()
	if s.hasExited() {
		return s.renderDelta(start, "exited"), nil
	}
	if err := s.writeInput(a.Input); err != nil {
		return "", err
	}
	return s.send(start, ctx)
}

const terminalSignalDescription = `Send an allowed POSIX signal to the foreground process group of a persistent terminal.

Allowed signals are SIGINT, SIGTERM, SIGHUP and SIGTSTP. SIGKILL is not offered: it would make it impossible to reap the shell cleanly; use terminal_close instead. Under a PTY, SIGINT interrupts the running foreground job just like Ctrl-C; the signal is also delivered to the shell's own process group. This is also how a running command is interrupted on the pipe fallback.

Args JSON schema:
{"type":"object","properties":{"id":{"type":"string","description":"Terminal session id."},"signal":{"type":"string","description":"Signal to deliver.","enum":["SIGINT","SIGTERM","SIGTSTP","SIGHUP"]}},"required":["id","signal"],"additionalProperties":false}`

type terminalSignalTool struct{ mgr *terminalManager }

func (t *terminalSignalTool) Name() string        { return "terminal_signal" }
func (t *terminalSignalTool) Description() string { return terminalSignalDescription }

// terminalSignalValues deliberately omits SIGKILL: the schema must not offer
// an option the switch below always rejects.
var terminalSignalValues = map[string]syscall.Signal{
	"SIGHUP":  syscall.Signal(1),
	"SIGINT":  syscall.Signal(2),
	"SIGTERM": syscall.Signal(15),
	"SIGTSTP": syscall.Signal(20),
}

func (t *terminalSignalTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Signal string `json:"signal"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("terminal_signal: id is required")
	}
	name := strings.ToUpper(strings.TrimSpace(a.Signal))
	sig, ok := terminalSignalValues[name]
	if !ok {
		return "", fmt.Errorf("terminal_signal: unknown signal %q; allowed signals are SIGHUP, SIGINT, SIGTERM, SIGTSTP (SIGKILL is rejected; use terminal_close)", a.Signal)
	}
	s, err := t.mgr.get(a.ID)
	if err != nil {
		return "", err
	}
	if s.hasExited() {
		return "", fmt.Errorf("terminal_signal: session %q has exited", a.ID)
	}
	targets := s.signalTargets()
	if len(targets) == 0 {
		return "", fmt.Errorf("terminal_signal: session %q has no process to signal", a.ID)
	}
	for _, target := range targets {
		if err := terminalDeliverSignal(target, sig); err != nil {
			return "", fmt.Errorf("terminal_signal: deliver %s to process group %d: %w", name, target, err)
		}
	}
	s.touch()
	if len(targets) > 1 {
		return fmt.Sprintf("delivered %s to process groups %d and %d of terminal %s", name, targets[0], targets[1], s.id), nil
	}
	return fmt.Sprintf("delivered %s to process group %d of terminal %s", name, targets[0], s.id), nil
}

const terminalCloseDescription = `Close one persistent terminal and wait until its process group is gone. Closing a terminal that has already exited is harmless.

Args JSON schema:
{"type":"object","properties":{"id":{"type":"string","description":"Terminal session id."}},"required":["id"],"additionalProperties":false}`

type terminalCloseTool struct{ mgr *terminalManager }

func (t *terminalCloseTool) Name() string        { return "terminal_close" }
func (t *terminalCloseTool) Description() string { return terminalCloseDescription }

func (t *terminalCloseTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("terminal_close: id is required")
	}
	s, err := t.mgr.get(a.ID)
	if err != nil {
		return "", err
	}
	closed, err := t.mgr.close(s)
	if err != nil {
		return "", fmt.Errorf("terminal_close: %w", err)
	}
	if !closed {
		return fmt.Sprintf("terminal session %s was already closing", a.ID), nil
	}
	return fmt.Sprintf("closed terminal session %s", a.ID), nil
}
