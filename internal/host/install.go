package host

// Install-on-restart: the update module schedules a verified download in
// <dataDir>/updates/pending.json; at the next process start the host installs
// the staged asset. The plan is kept if anything fails, so the next start can
// retry; a successful install removes both the plan and the staged file. This
// runs before the assembly/window exist, so a bad plan can never take the UI
// down with it (errors are logged, never fatal).
//
// The plan's "kind" selects the release asset layout:
//   - "binary" (also the fallback for a missing/unknown kind): the staged file
//     replaces the running executable.
//   - "appimage": the staged file replaces the AppImage named by $APPIMAGE
//     (falling back to binary replacement when the app was not started from an
//     AppImage).
//   - "archive": a tar.gz/.tgz/.zip containing the app dir (Windows/Linux) or
//     the .app bundle (macOS) is extracted and installed over the app.
//
// Windows cannot overwrite a running .exe, but it can rename it, so the
// windows paths use a rename-to-<exe>.old dance and the backup is removed
// best-effort at a later start.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/update"
)

// Host-side half of the pending.json schema (the update module writes it).
const (
	updatesDirName     = "updates"
	pendingInstallName = "pending.json"

	// pending install kinds. The update module writes one of them; a plan
	// written by an older version has none and installs as a binary.
	// "installer" (Windows NSIS setup) and "dmg" (macOS disk image) need a
	// user-visible launch: startup keeps them for the in-app install command
	// (see InstallPendingUpdateNow).
	installKindInstaller = "installer"
	installKindDMG       = "dmg"
	installKindBinary    = "binary"
	installKindAppImage  = "appimage"
	installKindArchive   = "archive"

	// installBackupSuffix names the previous executable/bundle kept by the
	// rename-dance paths until a later start can delete it.
	installBackupSuffix = ".old"

	installExecutableMode = 0o755
)

// pendingInstallFile mirrors the update module's pending.json. Kind is the
// release asset layout; a missing or unknown kind installs as a binary (the
// schema predates kinds, so existing pending plans keep working).
type pendingInstallFile struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Kind    string `json:"kind"`
}

// normalizeInstallKind maps a pending.json kind to one of the known install
// layouts; anything unrecognized (including "") installs as a binary.
func normalizeInstallKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case installKindInstaller:
		return installKindInstaller
	case installKindDMG:
		return installKindDMG
	case installKindAppImage:
		return installKindAppImage
	case installKindArchive:
		return installKindArchive
	default:
		return installKindBinary
	}
}

// ExecutableReplacer replaces the running executable with the staged update.
// The process uses defaultExecutableReplacer; tests inject fakes so neither the
// test binary nor the developer's binary is ever replaced.
type ExecutableReplacer interface {
	// CurrentExecutable reports the executable to install over.
	CurrentExecutable() (string, error)
	// Replace replaces that executable with the staged file. It returns an
	// error without touching the current executable when that is not possible.
	Replace(stagedPath string) error
}

// defaultExecutableReplacer replaces the executable in place: on Linux/macOS it
// copies the staged file to a temporary file next to the current executable and
// renames it over the target (atomic on the same filesystem; renaming over a
// running executable is allowed). Windows cannot replace a running .exe, so it
// renames the running executable to <exe>.old and copies the new file in. The
// staged file was SHA-256 verified when it was scheduled (and re-verified by
// the download).
type defaultExecutableReplacer struct{}

// CurrentExecutable implements ExecutableReplacer.
func (defaultExecutableReplacer) CurrentExecutable() (string, error) {
	return updateExecutable()
}

// Replace implements ExecutableReplacer.
func (defaultExecutableReplacer) Replace(stagedPath string) error {
	staged, err := validateStagedFile(stagedPath)
	if err != nil {
		return err
	}
	current, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("locate running executable: %w", err)
	}
	// Resolve a symlinked executable so the replacement targets the real binary.
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}
	return replaceExecutableFile(current, staged)
}

// replaceExecutableFile installs a staged binary over current. Linux/macOS
// replace atomically via rename; Windows renames the running .exe aside first
// (renaming is allowed while it runs) and copies the new file into place.
func replaceExecutableFile(current, staged string) error {
	if runtime.GOOS == "windows" {
		return replaceExecutableWithRenameDance(current, staged)
	}
	return copyFileAtomic(staged, current, installExecutableMode)
}

// replaceExecutableWithRenameDance performs the Windows-safe replacement: move
// the current executable to <exe>.old, then copy the staged file into place. If
// the copy fails the old executable is restored, so a failed install keeps a
// runnable app. The backup is left for the next start to clean up (a running
// Windows executable cannot always be deleted immediately).
func replaceExecutableWithRenameDance(current, staged string) error {
	backup := current + installBackupSuffix
	if err := os.Remove(backup); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale executable backup %q: %w", backup, err)
	}
	if err := os.Rename(current, backup); err != nil {
		return fmt.Errorf("move running executable %q aside: %w", current, err)
	}
	if err := copyFileAtomic(staged, current, installExecutableMode); err != nil {
		if restoreErr := os.Rename(backup, current); restoreErr != nil {
			return fmt.Errorf("copy new executable: %w (restore old executable: %v)", err, restoreErr)
		}
		return fmt.Errorf("copy new executable: %w", err)
	}
	return nil
}

// replaceAppImage atomically replaces the running AppImage with the staged
// file. Linux/macOS allow renaming a new file over the running AppImage.
func replaceAppImage(appImagePath, stagedPath string) error {
	target := strings.TrimSpace(appImagePath)
	if target == "" {
		return errors.New("APPIMAGE is empty")
	}
	// Resolve a symlinked AppImage so the replacement targets the real file.
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	return copyFileAtomic(stagedPath, target, installExecutableMode)
}

// installArchive installs a tar.gz/.tgz/.zip release asset. macOS swaps the
// whole .app bundle (the archive holds <Name>.app); Windows/Linux replace the
// files of the directory holding the running executable.
func installArchive(stagedPath, currentExecutable string) error {
	staged, err := validateStagedFile(stagedPath)
	if err != nil {
		return err
	}
	current := strings.TrimSpace(currentExecutable)
	if current == "" {
		return errors.New("current executable path is empty")
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}
	if runtime.GOOS == "darwin" {
		return installDarwinBundle(staged, current)
	}
	return installArchiveTree(staged, current)
}

// installDarwinBundle replaces <Name>.app (derived from the running executable
// path) with the single .app bundle inside the staged archive. The old bundle
// is moved aside first and restored if the new one cannot be moved in.
func installDarwinBundle(staged, current string) error {
	bundleRoot, err := darwinAppBundleRoot(current)
	if err != nil {
		return err
	}
	// The temp extraction dir sits next to the target bundle: the same
	// filesystem, and the same directory we need write access to for the swap.
	tempRoot, err := os.MkdirTemp(filepath.Dir(bundleRoot), ".zeokeel-update-*")
	if err != nil {
		return fmt.Errorf("create archive temp dir: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	if err := extractArchive(staged, tempRoot); err != nil {
		return err
	}
	extractedApp, err := singleAppBundle(tempRoot)
	if err != nil {
		return err
	}
	backup := bundleRoot + installBackupSuffix
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove stale app bundle backup %q: %w", backup, err)
	}
	if err := os.Rename(bundleRoot, backup); err != nil {
		return fmt.Errorf("move old app bundle aside: %w", err)
	}
	if err := os.Rename(extractedApp, bundleRoot); err != nil {
		if restoreErr := os.Rename(backup, bundleRoot); restoreErr != nil {
			return fmt.Errorf("move new app bundle into place: %w (restore old bundle: %v)", err, restoreErr)
		}
		return fmt.Errorf("move new app bundle into place: %w", err)
	}
	// The new bundle is in place. Keep the old bundle as <Name>.app.old until
	// the next start: the still-running process loads resources from its old
	// bundle path lazily, so deleting it now could break the current session.
	return nil
}

// installArchiveTree replaces the app files in the executable's directory from
// the archive's single top-level app directory. The running executable is
// replaced with the rename dance; every other file is written through a
// temporary file and renamed over the old one.
func installArchiveTree(staged, current string) error {
	targetDir := filepath.Dir(current)
	// The temp dir lives inside the target dir: that directory must be
	// writable for the install anyway, and it keeps extraction on the same
	// filesystem as the files being replaced.
	tempRoot, err := os.MkdirTemp(targetDir, ".zeokeel-update-*")
	if err != nil {
		return fmt.Errorf("create archive temp dir: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	if err := extractArchive(staged, tempRoot); err != nil {
		return err
	}
	appRoot, err := singleTopLevelDir(tempRoot)
	if err != nil {
		return err
	}
	return replaceDirectoryContents(appRoot, targetDir, current)
}

// replaceDirectoryContents copies the extracted app tree over targetDir,
// replacing existing files but leaving unrelated files alone. The running
// executable is moved aside (rename dance) rather than overwritten.
func replaceDirectoryContents(appRoot, targetDir, current string) error {
	currentAbs, err := filepath.Abs(current)
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	return filepath.WalkDir(appRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk extracted archive: %w", walkErr)
		}
		relative, err := filepath.Rel(appRoot, path)
		if err != nil {
			return fmt.Errorf("relativize archive entry: %w", err)
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(targetDir, relative)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat archive entry %q: %w", relative, err)
		}
		switch {
		case entry.IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return fmt.Errorf("create app directory %q: %w", relative, err)
			}
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			return replaceSymlink(path, target)
		case info.Mode().IsRegular():
			if targetAbs, absErr := filepath.Abs(target); absErr == nil && targetAbs == currentAbs {
				return replaceExecutableWithRenameDance(current, path)
			}
			if err := copyFileAtomic(path, target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("replace app file %q: %w", relative, err)
			}
			return nil
		default:
			return fmt.Errorf("unsupported archive entry %q", relative)
		}
	})
}

// replaceSymlink recreates an extracted symlink at target.
func replaceSymlink(source, target string) error {
	linkTarget, err := os.Readlink(source)
	if err != nil {
		return fmt.Errorf("read archive symlink %q: %w", filepath.Base(target), err)
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove old symlink %q: %w", target, err)
	}
	if err := os.Symlink(linkTarget, target); err != nil {
		return fmt.Errorf("create symlink %q: %w", target, err)
	}
	return nil
}

// darwinAppBundleRoot derives the <Name>.app root from a running bundle
// executable path (.../<Name>.app/Contents/MacOS/<binary>).
func darwinAppBundleRoot(executable string) (string, error) {
	current := strings.TrimSpace(executable)
	if current == "" {
		return "", errors.New("current executable path is empty")
	}
	macOSDir := filepath.Dir(current)
	contentsDir := filepath.Dir(macOSDir)
	bundleRoot := filepath.Dir(contentsDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" ||
		!strings.HasSuffix(filepath.Base(bundleRoot), ".app") {
		return "", fmt.Errorf("executable %q is not inside a macOS .app bundle (<Name>.app/Contents/MacOS/<binary>)", current)
	}
	return bundleRoot, nil
}

// singleAppBundle returns the only top-level .app directory of an extracted
// macOS archive; anything else is an unsupported layout.
func singleAppBundle(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read extracted archive: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() || !strings.HasSuffix(entries[0].Name(), ".app") {
		return "", errors.New("unsupported archive layout: want exactly one .app bundle at the archive root")
	}
	return filepath.Join(dir, entries[0].Name()), nil
}

// singleAppInDMG returns the single top-level .app bundle of a mounted macOS
// disk image. A real mygo dmg also contains .DS_Store, .VolumeIcon.icns and the
// /Applications symlink, so this deliberately ignores non-.app entries; the
// strict singleAppBundle variant stays for extracted archives.
func singleAppInDMG(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read mounted dmg: %w", err)
	}
	var apps []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") {
			apps = append(apps, filepath.Join(dir, entry.Name()))
		}
	}
	if len(apps) != 1 {
		return "", fmt.Errorf("unsupported dmg layout: want exactly one .app bundle, found %d", len(apps))
	}
	return apps[0], nil
}

// singleTopLevelDir returns the only top-level directory of an extracted
// archive (the app dir release assets contain); anything else is unsupported.
func singleTopLevelDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read extracted archive: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", errors.New("unsupported archive layout: want exactly one application directory at the archive root")
	}
	return filepath.Join(dir, entries[0].Name()), nil
}

// extractArchive unpacks a .tar.gz/.tgz/.zip release asset into destDir,
// rejecting entries that would escape it (path traversal).
func extractArchive(staged, destDir string) error {
	lower := strings.ToLower(staged)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(staged, destDir)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(staged, destDir)
	default:
		return fmt.Errorf("unsupported archive format %q (want .tar.gz, .tgz or .zip)", filepath.Base(staged))
	}
}

// extractTarGz unpacks a gzip-compressed tar archive.
func extractTarGz(staged, destDir string) error {
	file, err := os.Open(staged)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	uncompressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read gzip archive %q: %w", filepath.Base(staged), err)
	}
	defer uncompressed.Close()
	reader := tar.NewReader(uncompressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		target, err := archiveEntryTarget(destDir, header.Name)
		if err != nil {
			return err
		}
		mode := header.FileInfo().Mode()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode.Perm()|0o700); err != nil {
				return fmt.Errorf("create archive directory %q: %w", header.Name, err)
			}
		case tar.TypeReg:
			if err := writeArchiveFile(reader, target, mode.Perm()); err != nil {
				return fmt.Errorf("extract archive file %q: %w", header.Name, err)
			}
		case tar.TypeSymlink:
			if err := writeArchiveSymlink(header.Linkname, target, destDir); err != nil {
				return fmt.Errorf("extract archive symlink %q: %w", header.Name, err)
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			// PAX/GNU metadata entries carry no file content of their own.
			continue
		default:
			return fmt.Errorf("unsupported tar entry %q (type %d)", header.Name, header.Typeflag)
		}
	}
}

// extractZip unpacks a zip archive.
func extractZip(staged, destDir string) error {
	archive, err := zip.OpenReader(staged)
	if err != nil {
		return fmt.Errorf("open zip archive %q: %w", filepath.Base(staged), err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		target, err := archiveEntryTarget(destDir, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, entry.Mode().Perm()|0o700); err != nil {
				return fmt.Errorf("create archive directory %q: %w", entry.Name, err)
			}
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open archive entry %q: %w", entry.Name, err)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			linkTarget, readErr := io.ReadAll(io.LimitReader(source, 4096))
			closeErr := source.Close()
			if readErr != nil {
				return fmt.Errorf("read archive symlink %q: %w", entry.Name, readErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close archive symlink %q: %w", entry.Name, closeErr)
			}
			if err := writeArchiveSymlink(string(linkTarget), target, destDir); err != nil {
				return fmt.Errorf("extract archive symlink %q: %w", entry.Name, err)
			}
			continue
		}
		writeErr := writeArchiveFile(source, target, entry.Mode().Perm())
		closeErr := source.Close()
		if writeErr != nil {
			return fmt.Errorf("extract archive file %q: %w", entry.Name, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close archive file %q: %w", entry.Name, closeErr)
		}
	}
	return nil
}

// archiveEntryTarget joins an archive entry name to destDir after rejecting
// absolute names and path traversal.
func archiveEntryTarget(destDir, name string) (string, error) {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return "", errors.New("archive entry has an empty name")
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == "." {
		return destDir, nil
	}
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("archive entry %q has an absolute path", name)
	}
	target := filepath.Join(destDir, clean)
	relative, err := filepath.Rel(destDir, target)
	if err != nil {
		return "", fmt.Errorf("resolve archive entry %q: %w", name, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("archive entry %q escapes the extraction directory", name)
	}
	return target, nil
}

// writeArchiveFile writes one extracted regular file (creating its parent
// directory when the archive did not carry a dir entry for it).
func writeArchiveFile(source io.Reader, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	if _, err := io.Copy(file, source); err != nil {
		_ = file.Close()
		return fmt.Errorf("write archive file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close archive file: %w", err)
	}
	return nil
}

// writeArchiveSymlink creates an extracted symlink after rejecting a link
// target that points outside the extraction directory.
func writeArchiveSymlink(linkName, target, destDir string) error {
	link := strings.TrimSpace(linkName)
	if link == "" {
		return errors.New("symlink has an empty target")
	}
	if filepath.IsAbs(link) || filepath.VolumeName(link) != "" {
		return fmt.Errorf("symlink target %q is absolute", link)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(link)))
	relative, err := filepath.Rel(destDir, resolved)
	if err != nil {
		return fmt.Errorf("resolve symlink target %q: %w", link, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("symlink target %q escapes the extraction directory", link)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}
	if err := os.Symlink(link, target); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	return nil
}

// copyFileAtomic copies source over target by writing a temporary file in
// target's directory and renaming it into place, so target is never a partially
// written file. The temp file gets mode (the executable's 0755, or the archive
// entry's permission bits).
func copyFileAtomic(source, target string, mode os.FileMode) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat staged update: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged update %q is not a regular file", source)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".zeokeel-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create update temp file in %q: %w", filepath.Dir(target), err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	origin, err := os.Open(source)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("open staged update: %w", err)
	}
	_, copyErr := io.Copy(temp, origin)
	closeOriginErr := origin.Close()
	if copyErr != nil {
		_ = temp.Close()
		return fmt.Errorf("copy staged update: %w", copyErr)
	}
	if closeOriginErr != nil {
		_ = temp.Close()
		return fmt.Errorf("close staged update: %w", closeOriginErr)
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod update temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync update temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close update temp file: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("replace %q: %w", target, err)
	}
	committed = true
	return nil
}

// validateStagedFile trims a staged path and verifies it is an existing regular
// file.
func validateStagedFile(stagedPath string) (string, error) {
	staged := strings.TrimSpace(stagedPath)
	if staged == "" {
		return "", errors.New("staged update path is empty")
	}
	info, err := os.Stat(staged)
	if err != nil {
		return "", fmt.Errorf("stat staged update: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("staged update %q is not a regular file", staged)
	}
	return staged, nil
}

// pendingUpdate is a pending install after the plan metadata was read: the plan's
// identity plus the resolved staged path below <dataDir>/updates. SHA256 is the
// expected checksum; the staged bytes are only hashed when the plan is actually
// applied (see verifyPendingUpdate), so skipping an installer/dmg on startup
// does not read hundreds of megabytes (F10).
type pendingUpdate struct {
	// Version is the staged release version.
	Version string
	// Kind is the normalized install layout (installer/dmg/appimage/archive/
	// binary).
	Kind string
	// Staged is the resolved staged asset path (inside the updates directory).
	Staged string
	// SHA256 is the scheduled checksum the staged bytes must match.
	SHA256 string
}

// stagedFileSHA256 hashes one staged file; tests replace it to count hashes and
// pin the "hash only when applying" behavior.
var stagedFileSHA256 = fileSHA256

// readPendingPlan reads <dataDir>/updates/pending.json and validates the plan's
// structure and staged path containment, without hashing the asset. ok=false
// means there is no plan; an invalid plan is an error and the caller keeps it for
// a retry. Startup uses it to decide the kind first (F10).
func readPendingPlan(dataDir string) (pendingUpdate, bool, error) {
	if strings.TrimSpace(dataDir) == "" {
		return pendingUpdate{}, false, errors.New("no data directory for pending update")
	}
	updatesDir := filepath.Join(dataDir, updatesDirName)
	pendingPath := filepath.Join(updatesDir, pendingInstallName)
	body, err := os.ReadFile(pendingPath)
	if errors.Is(err, os.ErrNotExist) {
		return pendingUpdate{}, false, nil
	}
	if err != nil {
		return pendingUpdate{}, false, fmt.Errorf("read pending update: %w", err)
	}
	var pending pendingInstallFile
	if err := json.Unmarshal(body, &pending); err != nil {
		return pendingUpdate{}, false, fmt.Errorf("parse pending update: %w", err)
	}
	version := strings.TrimSpace(pending.Version)
	staged := strings.TrimSpace(pending.Path)
	if version == "" || staged == "" {
		return pendingUpdate{}, false, errors.New("pending update is incomplete (version/path)")
	}
	staged, err = stagingPathBelowUpdates(updatesDir, staged)
	if err != nil {
		return pendingUpdate{}, false, err
	}
	info, err := os.Stat(staged)
	if err != nil {
		return pendingUpdate{}, false, fmt.Errorf("stat staged update: %w", err)
	}
	if !info.Mode().IsRegular() {
		return pendingUpdate{}, false, fmt.Errorf("staged update %q is not a regular file", staged)
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(pending.SHA256))
	if expectedSHA == "" {
		return pendingUpdate{}, false, errors.New("pending update has no checksum")
	}
	return pendingUpdate{
		Version: version,
		Kind:    normalizeInstallKind(pending.Kind),
		Staged:  staged,
		SHA256:  expectedSHA,
	}, true, nil
}

// verifyPendingUpdate hashes the staged asset against the scheduled checksum.
// The schedule phase verified it, but the bytes on disk between schedule and
// install are untrusted: anything applied must be re-checked first.
func verifyPendingUpdate(pending pendingUpdate) (pendingUpdate, error) {
	actualSHA, err := stagedFileSHA256(pending.Staged)
	if err != nil {
		return pendingUpdate{}, fmt.Errorf("hash staged update: %w", err)
	}
	if !strings.EqualFold(actualSHA, pending.SHA256) {
		return pendingUpdate{}, fmt.Errorf("staged update checksum mismatch (want %s, got %s)", pending.SHA256, actualSHA)
	}
	return pending, nil
}

// readPendingUpdate reads, validates and checksum-verifies the pending install in
// one step: the immediate-install path must never hand an unverified asset to an
// installer. It hashes exactly once (F10).
func readPendingUpdate(dataDir string) (pendingUpdate, bool, error) {
	pending, ok, err := readPendingPlan(dataDir)
	if err != nil || !ok {
		return pendingUpdate{}, ok, err
	}
	verified, err := verifyPendingUpdate(pending)
	if err != nil {
		return pendingUpdate{}, false, err
	}
	return verified, true, nil
}

// clearPendingInstall removes the staged asset first and the consumed plan
// second (F9): when the staged delete fails (a running setup, an AV lock, a
// permission problem) the plan stays, so the next start retries the cleanup
// instead of leaving an orphan asset forever. It reports whether the plan was
// consumed; cleanup never fails an install.
func clearPendingInstall(dataDir, staged string, logger contracts.Logger) bool {
	if strings.TrimSpace(staged) != "" {
		if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			logInstallWarning(logger, "remove installed staged file %q (plan kept for a retry): %v", staged, err)
			return false
		}
	}
	pendingPath := filepath.Join(dataDir, updatesDirName, pendingInstallName)
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		logInstallWarning(logger, "remove installed pending plan %q: %v", pendingPath, err)
		return false
	}
	return true
}

// applyPendingUpdate applies a scheduled install that can run without a user
// gesture (binary/archive/appimage). Installer/dmg plans need the visible
// in-app install command and are refused here with the plan kept. It returns
// applied=true only after the install succeeded and the plan was cleaned up;
// every failure keeps pending.json (and the staged file) for a retry.
func applyPendingUpdate(dataDir string, replacer ExecutableReplacer, logger contracts.Logger) (bool, error) {
	pending, ok, err := readPendingPlan(dataDir)
	if err != nil || !ok {
		return false, err
	}
	return applyResolvedPendingUpdate(dataDir, pending, replacer, logger)
}

// applyResolvedPendingUpdate verifies and applies a plan whose metadata was
// already read (startup uses this to hash an applied plan exactly once, F10).
func applyResolvedPendingUpdate(dataDir string, pending pendingUpdate,
	replacer ExecutableReplacer, logger contracts.Logger) (bool, error) {
	if pending.Kind == installKindInstaller || pending.Kind == installKindDMG {
		return false, fmt.Errorf("pending update kind %q needs the in-app install command", pending.Kind)
	}
	if replacer == nil {
		return false, errors.New("no executable replacer")
	}
	verified, err := verifyPendingUpdate(pending)
	if err != nil {
		return false, err
	}
	current, err := replacer.CurrentExecutable()
	if err != nil {
		return false, fmt.Errorf("locate running executable: %w", err)
	}
	if err := applyPendingInstall(verified.Kind, verified.Staged, current, replacer); err != nil {
		return false, err
	}
	// The install is applied; clean up best effort. A leftover plan would try
	// to re-apply an already installed file on the next start.
	clearPendingInstall(dataDir, verified.Staged, logger)
	return true, nil
}

// applyPendingInstall dispatches the verified staged asset to the install
// layout selected by pending.json's kind.
func applyPendingInstall(kind, staged, current string, replacer ExecutableReplacer) error {
	switch kind {
	case installKindAppImage:
		if appImage := strings.TrimSpace(os.Getenv("APPIMAGE")); appImage != "" {
			// Renaming a new file over the running AppImage is allowed on
			// Linux/macOS; a staged AppImage is a self-contained executable.
			if err := replaceAppImage(appImage, staged); err != nil {
				return fmt.Errorf("replace AppImage: %w", err)
			}
			return nil
		}
		// Not started from an AppImage (or the variable was stripped): fall
		// back to replacing the executable.
		if err := replacer.Replace(staged); err != nil {
			return fmt.Errorf("replace executable: %w", err)
		}
		return nil
	case installKindArchive:
		if err := installArchive(staged, current); err != nil {
			return fmt.Errorf("install archive update: %w", err)
		}
		return nil
	default:
		if err := replacer.Replace(staged); err != nil {
			return fmt.Errorf("replace executable: %w", err)
		}
		return nil
	}
}

// stagingPathBelowUpdates resolves path and verifies it is below updatesDir.
func stagingPathBelowUpdates(updatesDir, path string) (string, error) {
	absUpdates, err := filepath.Abs(updatesDir)
	if err != nil {
		return "", fmt.Errorf("resolve updates dir: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve pending update path: %w", err)
	}
	relative, err := filepath.Rel(absUpdates, absPath)
	if err != nil {
		return "", fmt.Errorf("relativize pending update path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("pending update path %q is outside %q", absPath, absUpdates)
	}
	// Resolve symlinks before trusting the containment check.
	resolvedUpdates, err := filepath.EvalSymlinks(absUpdates)
	if err != nil {
		return "", fmt.Errorf("resolve updates dir symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve pending update symlinks: %w", err)
	}
	resolvedRelative, err := filepath.Rel(resolvedUpdates, resolvedPath)
	if err != nil {
		return "", fmt.Errorf("relativize resolved pending update path: %w", err)
	}
	if resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(resolvedRelative) {
		return "", fmt.Errorf("pending update path %q resolves outside %q", absPath, absUpdates)
	}
	return resolvedPath, nil
}

// fileSHA256 returns the lowercase hex SHA-256 of one file.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// cleanupOldExecutableBackup removes the <exe>.old backup owned by the
// rename-dance install. Only that exact name is touched; failures are logged,
// never fatal.
func cleanupOldExecutableBackup(currentExecutable string, logger contracts.Logger) {
	current := strings.TrimSpace(currentExecutable)
	if current == "" {
		return
	}
	backup := current + installBackupSuffix
	if err := os.Remove(backup); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logInstallWarning(logger, "remove stale executable backup %q: %v", backup, err)
	}
}

// cleanupStaleExecutableBackups is the startup half of the .old cleanup: the
// previous install could not delete the old executable while it was still
// running, so the next start removes it. The macOS bundle swap has the same
// shape: <Name>.app.old is kept until the process running from it has exited.
func cleanupStaleExecutableBackups(logger contracts.Logger) {
	current, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		current = resolved
	}
	cleanupOldExecutableBackup(current, logger)
	cleanupStaleAppBundleBackup(current, logger)
}

// cleanupStaleAppBundleBackup removes <Name>.app.old left by a macOS archive
// install, once the process that ran from the old bundle has exited. A path
// outside a .app bundle (every other platform) is a no-op.
func cleanupStaleAppBundleBackup(current string, logger contracts.Logger) {
	bundleRoot, err := darwinAppBundleRoot(current)
	if err != nil {
		return
	}
	backup := bundleRoot + installBackupSuffix
	if err := os.RemoveAll(backup); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logInstallWarning(logger, "remove stale app bundle backup %q: %v", backup, err)
	}
}

// clearInstalledPendingUpdate removes a plan whose version is already running:
// the immediate install quit the app into the new version (or the user finished
// a dmg install manually), so the plan and its staged asset are done. appVersion
// is the running product version passed by the host options (F17), not the
// package global: embedders override it and a plain build reports "dev", and an
// unparsable/older running version keeps the plan conservatively.
func clearInstalledPendingUpdate(dataDir, appVersion string, pending pendingUpdate, logger contracts.Logger) bool {
	running := strings.TrimSpace(appVersion)
	if running == "" || strings.TrimSpace(pending.Version) == "" {
		return false
	}
	comparison, err := update.CompareVersions(running, pending.Version)
	if err != nil || comparison < 0 {
		return false
	}
	if !clearPendingInstall(dataDir, pending.Staged, logger) {
		if logger != nil {
			logger.Printf("%s: downloaded update %s is already running (%s); its leftovers are locked, the plan stays for a cleanup retry",
				appName, pending.Version, running)
		}
		return true
	}
	if logger != nil {
		logger.Printf("%s: downloaded update %s is already running (%s); cleared the install plan",
			appName, pending.Version, running)
	}
	return true
}

// applyPendingUpdateOnStartup runs at process start and never fails startup: the
// outcome is logged and a failure simply keeps the plan for the next start. It
// reads the plan metadata first and hashes the staged asset only when it is
// actually applied: an installer/dmg is skipped without reading hundreds of
// megabytes, and an applied plan is verified exactly once (F10).
func applyPendingUpdateOnStartup(dataDir, appVersion string, logger contracts.Logger) {
	cleanupStaleExecutableBackups(logger)
	pending, ok, err := readPendingPlan(dataDir)
	if err != nil {
		if logger != nil {
			logger.Printf("%s: pending update install failed (plan kept): %v", appName, err)
		}
		return
	}
	if !ok {
		return
	}
	if clearInstalledPendingUpdate(dataDir, appVersion, pending, logger) {
		return
	}
	// A setup installer cannot run silently (and a dmg install needs the
	// running app to be swapped and reopened), so both wait for the user-visible
	// in-app install command: keep the plan instead of applying it on startup.
	if pending.Kind == installKindInstaller || pending.Kind == installKindDMG {
		if logger != nil {
			logger.Printf("%s: pending update %s (%s) waits for the in-app install command", appName, pending.Version, pending.Kind)
		}
		return
	}
	applied, err := applyResolvedPendingUpdate(dataDir, pending, defaultExecutableReplacer{}, logger)
	if err != nil {
		if logger != nil {
			logger.Printf("%s: pending update install failed (plan kept): %v", appName, err)
		}
		return
	}
	if applied && logger != nil {
		logger.Printf("%s: staged update installed; it takes effect on the next start", appName)
	}
}

// logInstallWarning logs a best-effort cleanup warning when a logger is present.
func logInstallWarning(logger contracts.Logger, format string, args ...any) {
	if logger != nil {
		logger.Printf("%s: update cleanup: "+format, append([]any{appName}, args...)...)
	}
}
