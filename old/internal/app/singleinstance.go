package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// Single-instance guard.
//
// A fixed loopback port used to decide "am I the only ZeoKeel?", which meant any
// local process (another user included) could squat 127.0.0.1:49731 and block
// every start, and two isolated data directories could never run side by side
// (RT-12/PD-03). The guard is now an exclusive lock file in the data directory:
// the OS releases the lock when the process dies, so there is no stale-lock
// cleanup, and the lock's scope is exactly the data directory it protects.
//
// The running instance also listens on an ephemeral loopback port recorded in
// the lock file. A second launch reads that record (0600 file in a 0700
// directory) and asks the running instance to show its window, so "start again"
// brings the existing app to the front instead of only printing a message. The
// fixed legacy port is still bound on a best-effort basis for compatibility
// with older builds; it is never authoritative.

const (
	// instanceLockFileName is the single-instance lock inside the data
	// directory. Its path is part of the contract (diagnostics name it).
	instanceLockFileName = "instance.lock"
	// legacyInstanceAddr is the fixed loopback port older ZeoKeel builds used
	// as their single-instance guard. Kept as a best-effort compatibility
	// listener; the data-directory lock is authoritative.
	legacyInstanceAddr = "127.0.0.1:49731"
)

// instanceLockInfo is the JSON payload in <data dir>/instance.lock. It lets a
// second launch name the running process and ask it to show its window without
// relying on any global name, port or socket path.
type instanceLockInfo struct {
	PID   int    `json:"pid"`
	Port  int    `json:"activation_port,omitempty"`
	Token string `json:"activation_token,omitempty"`
	Since string `json:"since,omitempty"`
}

// instanceActivationRequest is sent by a second launch to the first instance's
// loopback activation listener. The token comes from the 0600 lock file in the
// 0700 data directory, so a different local user cannot trigger it.
type instanceActivationRequest struct {
	Token string `json:"token"`
}

type instanceActivationResponse struct {
	OK bool `json:"ok"`
}

// instanceLock is the held single-instance state: the exclusively locked file,
// the activation listener and the best-effort legacy port.
type instanceLock struct {
	dir      string
	path     string
	file     *os.File
	token    string
	listener net.Listener
	legacy   net.Listener

	closeOnce sync.Once
	closeErr  error
}

// tryAcquireInstanceLock opens <dir>/instance.lock and takes the exclusive
// non-blocking OS lock. It returns (nil, info, nil) while another process holds
// it, where info is the best-effort owner record used for diagnostics and
// window activation. A nil error with a nil lock means "held by someone else".
func tryAcquireInstanceLock(dir string) (*instanceLock, *instanceLockInfo, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, instanceLockFileName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open instance lock: %w", err)
	}
	// A lock file written by an older build (or a backup/restore) may have
	// loose permissions; it sits next to the encrypted-nothing index and must
	// not be readable by other users either.
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("secure instance lock: %w", err)
	}
	locked, err := lockFileExclusive(file)
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("lock instance file: %w", err)
	}
	if !locked {
		info, _ := readInstanceLockInfo(path)
		_ = file.Close()
		return nil, info, nil
	}
	lock := &instanceLock{dir: dir, path: path, file: file}
	token, err := newInstanceToken()
	if err != nil {
		_ = lock.close()
		return nil, nil, err
	}
	lock.token = token
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = lock.close()
		return nil, nil, fmt.Errorf("start instance activation listener: %w", err)
	}
	lock.listener = ln
	info := instanceLockInfo{
		PID:   os.Getpid(),
		Port:  ln.Addr().(*net.TCPAddr).Port,
		Token: token,
		Since: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeInstanceLockInfo(file, info); err != nil {
		_ = lock.close()
		return nil, nil, fmt.Errorf("write instance lock record: %w", err)
	}
	return lock, nil, nil
}

// close releases the lock, listeners and file. It is idempotent.
func (l *instanceLock) close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.listener != nil {
			_ = l.listener.Close()
		}
		if l.legacy != nil {
			_ = l.legacy.Close()
		}
		if l.file != nil {
			if err := unlockFile(l.file); err != nil {
				l.closeErr = err
			}
			if err := l.file.Close(); err != nil && l.closeErr == nil {
				l.closeErr = err
			}
		}
	})
	return l.closeErr
}

// readInstanceLockInfo reads the owner record a running instance wrote. A
// missing or unreadable record is not fatal: the second launch then reports
// "already running" without being able to activate a window.
func readInstanceLockInfo(path string) (*instanceLockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info instanceLockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// writeInstanceLockInfo replaces the lock file contents. The calling process
// holds the exclusive lock, so no other instance reads the file mid-write.
func writeInstanceLockInfo(file *os.File, info instanceLockInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

// newInstanceToken returns the random capability a second launch must present
// before the running instance shows its window.
func newInstanceToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate instance token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// notifyExistingInstance asks the running process to show its window. It is
// best effort: a stale record or a refused connection only means the second
// launch reports "already running" without bringing a window to the front.
func notifyExistingInstance(info *instanceLockInfo) bool {
	if info == nil || info.Port <= 0 || strings.TrimSpace(info.Token) == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", info.Port), 700*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close() //nolint:errcheck // best effort
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(instanceActivationRequest{Token: info.Token}); err != nil {
		return false
	}
	var resp instanceActivationResponse
	if err := json.NewDecoder(io.LimitReader(conn, 512)).Decode(&resp); err != nil {
		return false
	}
	return resp.OK
}

// activationNote renders what the second launch did to the running instance.
func activationNote(activated bool) string {
	if activated {
		return "asked it to show its window"
	}
	return "switch to it from the taskbar or tray"
}

// acquire is the single-instance guard: it takes the data directory's exclusive
// lock file and starts the window-activation listener. A second launch returns
// a descriptive ErrAlreadyRunning (and asks the running instance to come to the
// front) instead of confusing a busy fixed port with a running app.
func (a *app) acquire() error {
	dir := DataDir()
	lock, info, err := tryAcquireInstanceLock(dir)
	if err != nil {
		return fmt.Errorf("zeokeel: single instance: %w", err)
	}
	if lock == nil {
		activated := notifyExistingInstance(info)
		if info != nil && info.PID > 0 {
			fmt.Fprintf(os.Stderr,
				"zeokeel: another ZeoKeel instance is already running for data directory %s (pid %d); %s\n",
				dir, info.PID, activationNote(activated))
			return fmt.Errorf("%w: data directory %s (pid %d)", ErrAlreadyRunning, dir, info.PID)
		}
		fmt.Fprintf(os.Stderr,
			"zeokeel: another ZeoKeel instance is already running for data directory %s; %s\n",
			dir, activationNote(activated))
		return fmt.Errorf("%w: data directory %s", ErrAlreadyRunning, dir)
	}
	a.instanceLock = lock
	go a.serveInstanceActivation()
	// Best effort legacy port: old builds treat a busy 49731 as "already
	// running", so holding it keeps an old second launch from starting a
	// duplicate. If the port is already taken (an older instance, or another
	// local process) the data-directory lock above is still authoritative and
	// startup continues: the old fixed-port denial of service is gone.
	if legacy, lerr := net.Listen("tcp", legacyInstanceAddr); lerr == nil {
		lock.legacy = legacy
	} else {
		log.Printf("zeokeel: legacy single-instance port %s unavailable (%v); continuing on the data-directory lock",
			legacyInstanceAddr, lerr)
	}
	return nil
}

// releaseInstance releases the single-instance lock and stops the activation
// listener. It is called on every exit path after a successful acquire.
func (a *app) releaseInstance() {
	if a.instanceLock != nil {
		_ = a.instanceLock.close()
		a.instanceLock = nil
	}
}

// serveInstanceActivation accepts second-launch activation requests. The
// listener is closed by releaseInstance, which makes Accept return.
func (a *app) serveInstanceActivation() {
	lock := a.instanceLock
	if lock == nil || lock.listener == nil {
		return
	}
	for {
		conn, err := lock.listener.Accept()
		if err != nil {
			return
		}
		go a.handleInstanceActivation(conn)
	}
}

// handleInstanceActivation authenticates one activation request and, when the
// token matches, brings the main window to the front.
func (a *app) handleInstanceActivation(conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best effort
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	var req instanceActivationRequest
	if err := json.NewDecoder(io.LimitReader(conn, 1024)).Decode(&req); err != nil {
		return
	}
	lock := a.instanceLock
	ok := lock != nil && subtle.ConstantTimeCompare([]byte(req.Token), []byte(lock.token)) == 1
	_ = json.NewEncoder(conn).Encode(instanceActivationResponse{OK: ok})
	if ok {
		a.requestActivation()
	}
}

// setActivationWindow records the main window used by second-launch
// activation. The function is safe to call from any goroutine: mygo marshals
// window calls to the UI thread. A request that arrived before the window
// existed is replayed once the window is known.
func (a *app) setActivationWindow(win *mygo.Window) {
	if win == nil {
		return
	}
	a.activationMu.Lock()
	a.activateWindow = func() {
		if win.IsMinimized() {
			win.Restore()
		}
		win.Show()
		win.Focus()
	}
	pending := a.activationPending
	a.activationPending = false
	fn := a.activateWindow
	a.activationMu.Unlock()
	if pending {
		fn()
	}
}

// requestActivation shows/focuses the main window on behalf of a second launch.
func (a *app) requestActivation() {
	a.activationMu.Lock()
	fn := a.activateWindow
	if fn == nil {
		a.activationPending = true
	}
	a.activationMu.Unlock()
	if fn != nil {
		fn()
	}
}
