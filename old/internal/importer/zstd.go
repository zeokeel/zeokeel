package importer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// zstdMagic is the first four bytes of a zstd frame (RFC 8878).
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// zstdAvailable reports whether the external zstd binary is installed.
func zstdAvailable() bool {
	_, err := exec.LookPath("zstd")
	return err == nil
}

// openSessionStream opens a DSH session file, transparently decompressing zstd
// sessions through the external zstd binary. A plain JSONL file is opened
// directly. The returned ReadCloser must be closed before the process exits;
// closing also reaps the decompressor.
//
// The importer deliberately does not pull a Go zstd dependency into the module
// (the project keeps a small dependency set and adding one would change
// go.mod): an explicit, user-visible ErrZstdUnavailable is preferable to a
// silent skip. A future pure-Go decoder can replace this function without
// touching the parser.
func openSessionStream(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("importer: open session %q: %w", path, err)
	}
	head := make([]byte, len(zstdMagic))
	n, _ := io.ReadFull(f, head)
	compressed := n == len(zstdMagic) && string(head) == string(zstdMagic)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("importer: seek session %q: %w", path, err)
	}
	if !compressed {
		return f, nil
	}
	_ = f.Close()

	zstd, err := exec.LookPath("zstd")
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %w", ErrZstdUnavailable, path, err)
	}
	// No shell is involved: zstd is exec'd directly with a fixed flag and the
	// session path as one argument.
	cmd := exec.Command(zstd, "-dc", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("importer: zstd pipe for %q: %w", path, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("importer: start zstd for %q: %w", path, err)
	}
	return &zstdStream{stdout: stdout, cmd: cmd, stderr: &stderr, path: path}, nil
}

// zstdStream is the decompressed stdout pipe plus the zstd process, reaped by
// Close so a parse error cannot leak the child.
type zstdStream struct {
	stdout io.ReadCloser
	cmd    *exec.Cmd
	stderr *strings.Builder
	path   string
	closed bool
}

func (z *zstdStream) Read(p []byte) (int, error) { return z.stdout.Read(p) }

// Close closes the pipe and waits for zstd, reporting a decompression failure
// (for example a corrupted frame) as an error.
func (z *zstdStream) Close() error {
	if z == nil || z.closed {
		return nil
	}
	z.closed = true
	_ = z.stdout.Close()
	waitErr := z.cmd.Wait()
	if waitErr != nil {
		detail := strings.TrimSpace(z.stderr.String())
		if detail == "" {
			detail = waitErr.Error()
		}
		return fmt.Errorf("importer: decompress %q: %s", z.path, detail)
	}
	return nil
}

// IsZstdUnavailable reports whether err is the missing-zstd sentinel.
func IsZstdUnavailable(err error) bool { return errors.Is(err, ErrZstdUnavailable) }
