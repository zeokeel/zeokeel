package importer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/store"
)

// DSH attachment migration (residual PD-04 item).
//
// DSH stores prompt images and file attachments outside the session log as
// content-addressed objects below <DSH home>/attachments/v1:
//
//	objects/<sha256[0:2]>/<sha256>            normalized image bytes
//	file-objects/<sha256[0:2]>/<sha256>       verbatim file bytes
//	files/<sha256[0:2]>/<sha256>/<name>       display-name alias of a file
//
// Message content references them as
// {"type":"image","attachment":{"attachmentId":"sha256:...","mediaType":"image/png",...}}.
// Older exports may embed the bytes as base64 instead; both forms are accepted.
//
// The importer copies the object bytes into <ZeoKeel data dir>/attachments/
// <session id>/ and creates one store attachment row linked to the imported
// message (MessageID). The message model itself is not extended: images never
// become message text. Sources that are missing, above the per-file cap or
// corrupt (unreadable, bad base64, digest/declared-size mismatch, or a content
// part with no resolvable reference) are counted as skips instead of failing
// the session, because one bad historical attachment must not lose a whole
// conversation.
const (
	// maxAttachmentBytes matches session.MaxAttachmentBytes (25 MiB): the app's
	// per-file attachment cap. A larger DSH object is counted as skipped rather
	// than imported into a store the app would refuse to re-read.
	maxAttachmentBytes int64 = 25 << 20
	// maxAttachmentFileName bounds the display-name component used when copying
	// an object into the ZeoKeel attachment directory.
	maxAttachmentFileName = 80
	// attachmentKindImage/attachmentKindFile are the DSH content part kinds
	// (also used for skip-reason routing).
	attachmentKindImage = "image"
	attachmentKindFile  = "file"
	// Attachment source problem classes (not skip reasons themselves).
	attachmentProblemMissing   = "missing"
	attachmentProblemOversized = "oversized"
	attachmentProblemCorrupt   = "corrupt"
)

// resolvedAttachment is one content attachment whose source bytes were read and
// validated, ready to be copied into the ZeoKeel store.
type resolvedAttachment struct {
	Name string
	MIME string
	Kind string
	Size int64
	Data []byte
}

// attachmentOutcome is the result of resolving one ContentAttachment: either a
// resolved source (problem == "") or a problem class for the skip counters.
type attachmentOutcome struct {
	resolved resolvedAttachment
	problem  string
}

// attachmentCounts accumulates attachment migration/skip numbers for one run
// (and for one session before being merged into the report).
type attachmentCounts struct {
	imagesMigrated      int
	attachmentsMigrated int
	imagesDropped       int
	attachmentsDropped  int
	skips               map[string]int
}

// noteSkip increments one granular skip counter.
func (c *attachmentCounts) noteSkip(reason string) {
	if c.skips == nil {
		c.skips = make(map[string]int)
	}
	c.skips[reason]++
}

// record updates the counters for one attachment outcome. kind is the DSH
// content part kind ("image" or "file").
func (c *attachmentCounts) record(kind string, outcome attachmentOutcome) {
	if outcome.problem == "" {
		if kind == attachmentKindImage {
			c.imagesMigrated++
		} else {
			c.attachmentsMigrated++
		}
		return
	}
	if kind == attachmentKindImage {
		c.imagesDropped++
		c.noteSkip(SkipImagesDropped)
		c.noteSkip(imageProblemReason(outcome.problem))
		return
	}
	c.attachmentsDropped++
	c.noteSkip(SkipAttachmentsDropped)
	c.noteSkip(fileProblemReason(outcome.problem))
}

// mergeInto adds the counters to the run report.
func (c *attachmentCounts) mergeInto(r *Report) {
	if r == nil {
		return
	}
	r.ImagesMigrated += c.imagesMigrated
	r.AttachmentsMigrated += c.attachmentsMigrated
	r.ImagesDropped += c.imagesDropped
	r.AttachmentsDropped += c.attachmentsDropped
	for reason, n := range c.skips {
		r.noteSkip(reason, n)
	}
}

func imageProblemReason(problem string) string {
	switch problem {
	case attachmentProblemMissing:
		return SkipImageMissing
	case attachmentProblemOversized:
		return SkipImageOversized
	default:
		return SkipImageCorrupt
	}
}

func fileProblemReason(problem string) string {
	switch problem {
	case attachmentProblemMissing:
		return SkipAttachmentMissing
	case attachmentProblemOversized:
		return SkipAttachmentOversized
	default:
		return SkipAttachmentCorrupt
	}
}

// resolveAttachment validates one message attachment against the DSH attachment
// root. Source problems are reported as a problem class, never as a hard error:
// the run continues and the report explains what was skipped.
func resolveAttachment(a ContentAttachment, attachmentsRoot string) attachmentOutcome {
	kind := a.Kind
	if kind != attachmentKindFile {
		kind = attachmentKindImage
	}
	data, mediaType, name, problem := loadAttachmentSource(a, attachmentsRoot)
	if problem != "" {
		return attachmentOutcome{problem: problem}
	}
	if int64(len(data)) > maxAttachmentBytes {
		return attachmentOutcome{problem: attachmentProblemOversized}
	}
	out := resolvedAttachment{Size: int64(len(data)), Data: data}
	out.Name = sanitizeAttachmentFileName(name, defaultAttachmentName(kind, mediaType))
	out.MIME = normalizeAttachmentMIME(mediaType)
	if out.MIME == "" {
		out.MIME = detectAttachmentMIME(out.Name, data)
	}
	if kind == attachmentKindImage {
		out.Kind = store.AttachmentKindImage
		if !strings.HasPrefix(out.MIME, "image/") {
			if sniffed := detectAttachmentMIME(out.Name, data); strings.HasPrefix(sniffed, "image/") {
				out.MIME = sniffed
			}
		}
		return attachmentOutcome{resolved: out}
	}
	// DSH "file" parts are generic files: classify by name/media type so text
	// files become text attachments (prompt-injectable) and the rest stay files.
	out.Kind = attachmentKindFor(out.Name, out.MIME)
	return attachmentOutcome{resolved: out}
}

// loadAttachmentSource reads the bytes for one content attachment from the DSH
// attachment store (preferring the durable object) or from its inline base64
// payload. The returned problem class is empty on success.
func loadAttachmentSource(a ContentAttachment, attachmentsRoot string) (data []byte, mediaType, name, problem string) {
	mediaType = strings.TrimSpace(a.MediaType)
	name = strings.TrimSpace(a.Name)
	if strings.TrimSpace(a.AttachmentID) != "" {
		if digest, ok := parseAttachmentDigest(a.AttachmentID); ok {
			for _, candidate := range attachmentObjectCandidates(attachmentsRoot, a.Kind, digest, name) {
				// Lstat, not Stat: a content-addressed DSH object must itself be
				// a regular file. Following a symlink would let a crafted export
				// read arbitrary local files into the imported session, so links
				// (and devices/fifos) are not valid sources.
				info, err := os.Lstat(candidate)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return nil, "", "", attachmentProblemCorrupt
				}
				if !info.Mode().IsRegular() {
					continue
				}
				if info.Size() > maxAttachmentBytes {
					return nil, "", "", attachmentProblemOversized
				}
				buf, err := os.ReadFile(candidate)
				if err != nil {
					return nil, "", "", attachmentProblemCorrupt
				}
				if !digestMatches(digest, buf) {
					return nil, "", "", attachmentProblemCorrupt
				}
				if a.DeclaredBytes > 0 && a.DeclaredBytes != int64(len(buf)) {
					return nil, "", "", attachmentProblemCorrupt
				}
				return buf, mediaType, name, ""
			}
			if a.InlineData == "" {
				return nil, "", "", attachmentProblemMissing
			}
			// A durable reference whose object is gone may still carry the
			// inline fallback in hand-made exports; prefer it over dropping.
		}
		if a.InlineData == "" {
			// An attachment id that is not a sha256 digest cannot be resolved.
			return nil, "", "", attachmentProblemCorrupt
		}
	}
	if a.InlineData != "" {
		buf, err := decodeAttachmentBase64(a.InlineData)
		if err != nil {
			return nil, "", "", attachmentProblemCorrupt
		}
		if a.DeclaredBytes > 0 && a.DeclaredBytes != int64(len(buf)) {
			return nil, "", "", attachmentProblemCorrupt
		}
		return buf, mediaType, name, ""
	}
	// Nothing references bytes: a bare {"type":"image"} part is not migratable.
	return nil, "", "", attachmentProblemCorrupt
}

// attachmentObjectCandidates lists the DSH object locations for one digest, in
// preference order. The versioned v1 root is what current DSH builds write;
// unversioned layouts and the file alias are accepted for older data/backups.
func attachmentObjectCandidates(attachmentsRoot, kind, digest, name string) []string {
	root := strings.TrimSpace(attachmentsRoot)
	if root == "" {
		return nil
	}
	shard := digest[:2]
	var candidates []string
	add := func(parts ...string) {
		candidates = append(candidates, filepath.Join(append([]string{root}, parts...)...))
	}
	if kind == attachmentKindFile {
		add("v1", "file-objects", shard, digest)
		add("file-objects", shard, digest)
		add("v1", "objects", shard, digest)
		add("objects", shard, digest)
		safe := sanitizeAttachmentFileName(name, "")
		if safe != "" {
			add("v1", "files", shard, digest, safe)
			add("files", shard, digest, safe)
		}
		return candidates
	}
	add("v1", "objects", shard, digest)
	add("objects", shard, digest)
	return candidates
}

// parseAttachmentDigest normalizes a DSH attachment id into a lowercase hex
// sha256 digest. "sha256:<hex>" and bare 64-char hex are accepted.
func parseAttachmentDigest(id string) (string, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) != sha256.Size*2 {
		return "", false
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", false
	}
	return id, true
}

// digestMatches reports whether data hashes to the content-addressed digest.
func digestMatches(digest string, data []byte) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == digest
}

// decodeAttachmentBase64 decodes canonical (or unpadded) standard base64.
func decodeAttachmentBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if data, err := base64.StdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// storeMigratedAttachment copies one resolved attachment into the ZeoKeel data
// directory and creates its store row, linked to messageID. On a store error the
// copied file is removed so no orphan is left behind.
func storeMigratedAttachment(st *store.Store, sessionID, messageID string, a resolvedAttachment, createdAt time.Time) (*store.Attachment, error) {
	if st == nil {
		return nil, fmt.Errorf("importer: store attachment %q: no destination store", a.Name)
	}
	dir, err := st.AttachmentDir(sessionID)
	if err != nil {
		return nil, fmt.Errorf("importer: attachment directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("importer: create attachment directory: %w", err)
	}
	id, err := newAttachmentID()
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(dir, id+"-"+a.Name)
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("importer: create attachment copy: %w", err)
	}
	if _, err := f.Write(a.Data); err != nil {
		_ = f.Close()
		_ = os.Remove(dest)
		return nil, fmt.Errorf("importer: write attachment copy: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return nil, fmt.Errorf("importer: close attachment copy: %w", err)
	}
	row, err := st.CreateAttachment(store.Attachment{
		ID:        id,
		SessionID: sessionID,
		MessageID: messageID,
		Name:      a.Name,
		Path:      dest,
		MIME:      a.MIME,
		Kind:      a.Kind,
		Size:      a.Size,
		CreatedAt: createdAt,
	})
	if err != nil {
		_ = os.Remove(dest)
		return nil, fmt.Errorf("importer: store attachment %q: %w", a.Name, err)
	}
	return row, nil
}

// newAttachmentID returns a random 32-char hex id for one migrated attachment.
func newAttachmentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("importer: generate attachment id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// defaultAttachmentName derives a display name when the DSH part has none.
func defaultAttachmentName(kind, mediaType string) string {
	if kind != attachmentKindImage {
		return "attachment"
	}
	mediaType = normalizeAttachmentMIME(mediaType)
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return "image" + exts[0]
	}
	return "image"
}

// normalizeAttachmentMIME strips parameters and lowercases a media type.
func normalizeAttachmentMIME(mediaType string) string {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// detectAttachmentMIME prefers the file extension and falls back to sniffing the
// first bytes.
func detectAttachmentMIME(name string, data []byte) string {
	if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
		if mediaType := mime.TypeByExtension(ext); mediaType != "" {
			return normalizeAttachmentMIME(mediaType)
		}
	}
	if len(data) > 0 {
		return normalizeAttachmentMIME(http.DetectContentType(data))
	}
	return "application/octet-stream"
}

// attachmentKindFor classifies a generic file part as text, image or file.
func attachmentKindFor(name, mediaType string) string {
	mediaType = normalizeAttachmentMIME(mediaType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	if strings.HasPrefix(mediaType, "image/") {
		return store.AttachmentKindImage
	}
	if strings.HasPrefix(mediaType, "text/") {
		return store.AttachmentKindText
	}
	switch mediaType {
	case "application/json", "application/xml", "application/yaml",
		"application/x-yaml", "application/toml", "application/javascript":
		return store.AttachmentKindText
	}
	if textAttachmentExtensions[strings.ToLower(filepath.Ext(name))] {
		return store.AttachmentKindText
	}
	return store.AttachmentKindFile
}

// textAttachmentExtensions are formats whose extension alone marks a file as
// text even when the platform MIME table has no registration.
var textAttachmentExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true,
	".jsonl": true, ".ndjson": true, ".yaml": true, ".yml": true,
	".toml": true, ".csv": true, ".tsv": true, ".xml": true,
	".html": true, ".htm": true, ".css": true, ".scss": true,
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true,
	".jsx": true, ".go": true, ".py": true, ".rb": true, ".rs": true,
	".java": true, ".kt": true, ".c": true, ".h": true, ".cpp": true,
	".hpp": true, ".sh": true, ".bash": true, ".zsh": true, ".sql": true,
	".log": true, ".ini": true, ".cfg": true, ".conf": true, ".env": true,
}

// sanitizeAttachmentFileName makes a DSH display name safe as one path
// component: directory separators are stripped (no escape from the attachment
// directory) and anything outside [A-Za-z0-9._-] becomes '_'. Empty or
// dot-only names fall back to fallback.
func sanitizeAttachmentFileName(name, fallback string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = path.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return fallback
	}
	if len(out) > maxAttachmentFileName {
		out = out[len(out)-maxAttachmentFileName:]
	}
	return out
}
