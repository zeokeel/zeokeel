package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zeokeel/zeokeel/internal/gateway/webclient"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Listener defaults (docs/TRANSPORT.md §2.5).
const (
	// DefaultPort is the first port the gateway tries.
	DefaultPort = 9932
	// portTries is how many consecutive ports are tried before the kernel is
	// asked for any free port. A fixed, documented range keeps the URL stable
	// across restarts of the same machine without turning the gateway into a
	// port scanner.
	portTries = 10
	// listenHost is hard-coded: the gateway is a loopback-only service (§1.2
	// N1). It never listens on 0.0.0.0 and has no TLS.
	listenHost = "127.0.0.1"
)

// File names inside the data directory.
const (
	// InfoFileName is the discovery file the tray shell and second launches
	// read. It carries the port and the token and is written 0600.
	InfoFileName = "gateway.json"
	// LockFileName is the single-instance flock.
	LockFileName = "gateway.lock"
)

// InfoSchemaVersion is the gateway.json document version.
const InfoSchemaVersion = 1

// Info is the contents of <data-dir>/gateway.json (docs/TRANSPORT.md §2.4).
// Readers (the tray shell) must prefer this file over guessing a port; the
// token is here because the file is the only trusted channel between the
// gateway process and the user's browser launch.
type Info struct {
	SchemaVersion int    `json:"schemaVersion"`
	Epoch         string `json:"epoch"`
	PID           int    `json:"pid"`
	Port          int    `json:"port"`
	Token         string `json:"token"`
	StartedAt     string `json:"startedAt"`
	Product       string `json:"product"`
	Version       string `json:"version"`
	// BundleSHA256 / BundleBuiltAt identify the browser client this binary
	// serves. Commit times and build times drift (a bundle can be built before
	// the commit that ships it), so verification compares content, not timestamps
	// (round-4 conn audit, note 1).
	BundleSHA256  string `json:"bundleSha256,omitempty"`
	BundleBuiltAt string `json:"bundleBuiltAt,omitempty"`
}

// Origin is the exact scheme+host+port the browser client is served from. Host
// and Origin checks compare against it verbatim (§3.2 C2/C3).
func (i Info) Origin() string {
	return "http://" + listenHost + ":" + strconv.Itoa(i.Port)
}

// URL is the address handed to the user: the token travels in the fragment, so
// it never reaches the server, an access log or a Referer header (§3.1).
func (i Info) URL() string {
	return i.Origin() + "/#token=" + i.Token
}

// infoPath is the absolute path of one gateway bookkeeping file.
func infoPath(dataDir, name string) string {
	return filepath.Join(dataDir, name)
}

// writeInfo atomically writes gateway.json (tmp + fsync + rename, 0600).
func writeInfo(dataDir string, info Info) error {
	payload, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("gateway: encode %s: %w", InfoFileName, err)
	}
	path := filepath.Join(dataDir, InfoFileName)
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("gateway: create %s: %w", tmp, err)
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("gateway: write %s: %w", tmp, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("gateway: sync %s: %w", tmp, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("gateway: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("gateway: publish %s: %w", path, err)
	}
	return nil
}

// ReadInfo reads the running gateway's <data-dir>/gateway.json. It is the
// supported way for another in-process component (the tray shell) to learn the
// port and token; a missing file means no gateway is running.
func ReadInfo(dataDir string) (Info, error) { return readInfo(dataDir) }

// readInfo reads <data-dir>/gateway.json. A missing or unreadable file is
// reported as an error so callers can distinguish "no gateway" from a bad port.
func readInfo(dataDir string) (Info, error) {
	payload, err := os.ReadFile(filepath.Join(dataDir, InfoFileName))
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(payload, &info); err != nil {
		return Info{}, fmt.Errorf("gateway: parse %s: %w", InfoFileName, err)
	}
	return info, nil
}

// removeInfo deletes gateway.json on shutdown, ignoring a missing file.
func removeInfo(dataDir string) {
	_ = os.Remove(filepath.Join(dataDir, InfoFileName))
}

// listen binds the gateway listener: the requested port first, then the next
// nine, then a kernel-assigned port. Every attempt binds the loopback address
// only.
func listen(requested int) (net.Listener, error) {
	if requested < 0 {
		// Tests ask the kernel directly; production never passes a negative
		// port.
		listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, "0"))
		if err != nil {
			return nil, fmt.Errorf("gateway: bind %s:0: %w", listenHost, err)
		}
		return listener, nil
	}
	base := requested
	if base == 0 {
		base = DefaultPort
	}
	var lastErr error
	for i := 0; i < portTries; i++ {
		address := net.JoinHostPort(listenHost, strconv.Itoa(base+i))
		listener, err := net.Listen("tcp", address)
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	// Every preferred port is taken (or unbindable): let the kernel choose. The
	// URL is only published through gateway.json, so an arbitrary port is fine.
	listener, err := net.Listen("tcp", net.JoinHostPort(listenHost, "0"))
	if err != nil {
		if lastErr != nil {
			return nil, fmt.Errorf("gateway: bind %s:%d..%d and :0: %w", listenHost, base, base+portTries-1, err)
		}
		return nil, fmt.Errorf("gateway: bind %s:0: %w", listenHost, err)
	}
	return listener, nil
}

// randomToken returns a fresh process token: 32 random bytes, base64url (43
// characters, no padding), and an 8-byte hex epoch (§3.1).
func randomToken() (token, epoch string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("gateway: generate token: %w", err)
	}
	epochRaw := make([]byte, 8)
	if _, err := rand.Read(epochRaw); err != nil {
		return "", "", fmt.Errorf("gateway: generate epoch: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), hex.EncodeToString(epochRaw), nil
}

// errAlreadyRunning marks the "another gateway owns the data directory" case:
// the second launch exits 0 after printing the running instance's URL (§2.4).
var errAlreadyRunning = errors.New("gateway: another instance is already running")

// alreadyRunningError carries the running instance's published info.
type alreadyRunningError struct {
	Info Info
}

func (e *alreadyRunningError) Error() string {
	return "another instance is already running at " + e.Info.Origin()
}

func (e *alreadyRunningError) Unwrap() error { return errAlreadyRunning }

// newInfo builds the published description of this process.
func newInfo(epoch string, port int, token, product, version string) Info {
	bundleSha, bundleBuiltAt := "", ""
	if info, ok := webclient.Fingerprint(); ok {
		bundleSha, bundleBuiltAt = info.SHA256, info.BuiltAt
	}
	return Info{
		BundleSHA256:  bundleSha,
		BundleBuiltAt: bundleBuiltAt,
		SchemaVersion: InfoSchemaVersion,
		Epoch:         epoch,
		PID:           os.Getpid(),
		Port:          port,
		Token:         token,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		Product:       product,
		Version:       version,
	}
}
