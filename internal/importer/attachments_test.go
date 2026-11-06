package importer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// image/attachment migration (residual PD-04 item): DSH stores prompt images
// and files as content-addressed objects under <DSH home>/attachments/v1 and
// references them from message content parts. The importer must migrate the
// objects into ZeoKeel's attachment rows (linked to the ZeoKeel message) while
// keeping the text-only message model, and must count (not fail on) missing,
// oversized or corrupt sources.

// pngBytes is a tiny but structurally plausible PNG payload. The importer never
// decodes image pixels: media type and byte identity come from the DSH
// reference, so these bytes are enough to exercise the migration.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeDSHObject writes data into the durable, content-addressed DSH attachment
// store and returns the sha256 digest plus the object path.
func writeDSHObject(t *testing.T, home, area string, data []byte) (string, string) {
	t.Helper()
	hexID := digestHex(data)
	dir := filepath.Join(home, "attachments", "v1", area, hexID[:2])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir attachment object dir: %v", err)
	}
	path := filepath.Join(dir, hexID)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write attachment object: %v", err)
	}
	return hexID, path
}

// dshImagePart writes an image object and returns the DSH content part that
// references it (the format DSH 0.2.x writes into user/message content).
func dshImagePart(t *testing.T, home string, data []byte, name, mediaType string, width, height int) map[string]any {
	t.Helper()
	hexID, _ := writeDSHObject(t, home, "objects", data)
	return map[string]any{
		"type": "image",
		"attachment": map[string]any{
			"attachmentId": "sha256:" + hexID,
			"mediaType":    mediaType,
			"bytes":        len(data),
			"width":        width,
			"height":       height,
			"name":         name,
		},
	}
}

// dshFilePart writes a file object and returns the DSH content part that
// references it (file attachments are stored byte-for-byte in file-objects/).
func dshFilePart(t *testing.T, home string, data []byte, name string) map[string]any {
	t.Helper()
	hexID, _ := writeDSHObject(t, home, "file-objects", data)
	return map[string]any{
		"type": "file",
		"attachment": map[string]any{
			"attachmentId": "sha256:" + hexID,
			"name":         name,
			"bytes":        len(data),
		},
	}
}

// userMessagePartsEvent builds one genuine human user/message event whose
// content carries arbitrary parts (text and image/file attachments).
func userMessagePartsEvent(t *testing.T, seq int64, parts []map[string]any) string {
	t.Helper()
	return eventLine(t, "user/message", seq, map[string]any{
		"role":    "user",
		"source":  map[string]any{"kind": "user"},
		"content": parts,
	})
}

// singleImportedSession returns the only imported ZeoKeel session of report.
func singleImportedSession(t *testing.T, st *store.Store, report *Report) store.Session {
	t.Helper()
	if len(report.Workspaces) != 1 || report.Workspaces[0].WorkspaceID == "" {
		t.Fatalf("workspace report = %+v", report.Workspaces)
	}
	sessions, err := st.ListSessions(report.Workspaces[0].WorkspaceID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("imported sessions = %d, want 1", len(sessions))
	}
	return sessions[0]
}

// TestImportMigratesImageAttachment is the core image-migration contract: the
// DSH image object becomes a ZeoKeel attachment row of kind image, linked to
// the imported user message, with the bytes copied into the ZeoKeel data
// directory. The message model stays text-only (no image blob in messages).
func TestImportMigratesImageAttachment(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	sessionFile := writeFixtureSession(t, home, "--data-proj--", "session-image", "session.v4.jsonl", []string{
		headerLine(t, "session-image", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{
			{"type": "text", "text": "look at this diagram"},
			dshImagePart(t, home, pngBytes, "diagram.png", "image/png", 1, 1),
		}),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "nice diagram"}}),
	})
	_ = sessionFile

	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsImported != 1 || report.MessagesImported != 2 {
		t.Fatalf("report = %+v, want one session with two messages", report)
	}
	if report.ImagesMigrated != 1 {
		t.Fatalf("ImagesMigrated = %d, want 1 (report %+v)", report.ImagesMigrated, report)
	}
	if report.ImagesDropped != 0 || report.Skips[SkipImagesDropped] != 0 {
		t.Fatalf("images dropped = %d skips %v, want none", report.ImagesDropped, report.Skips)
	}

	sess := singleImportedSession(t, st, report)
	messages, err := st.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].Content != "look at this diagram" {
		t.Fatalf("messages = %+v", messages)
	}
	if strings.Contains(messages[0].Content, "data:image") || strings.Contains(messages[0].Content, "base64") {
		t.Fatalf("image bytes leaked into message content: %q", messages[0].Content)
	}

	attachments, err := st.ListAttachments(sess.ID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1: %+v", len(attachments), attachments)
	}
	a := attachments[0]
	if a.Kind != store.AttachmentKindImage || a.MIME != "image/png" || a.Name != "diagram.png" {
		t.Fatalf("attachment = %+v, want kind image, image/png, diagram.png", a)
	}
	if a.MessageID != messages[0].ID {
		t.Fatalf("attachment message id = %q, want the imported user message %q", a.MessageID, messages[0].ID)
	}
	if a.Size != int64(len(pngBytes)) {
		t.Fatalf("attachment size = %d, want %d", a.Size, len(pngBytes))
	}
	wantDir := filepath.Join(st.Dir(), "attachments", sess.ID)
	if filepath.Dir(a.Path) != wantDir {
		t.Fatalf("attachment path = %q, want directory %q", a.Path, wantDir)
	}
	data, err := st.ReadAttachmentData(a, 0)
	if err != nil {
		t.Fatalf("ReadAttachmentData: %v", err)
	}
	if string(data) != string(pngBytes) {
		t.Fatalf("stored attachment bytes differ from the DSH object (got %d bytes)", len(data))
	}

	// The JSON report must expose the migration counter additively while the
	// historical field names stay intact.
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["images_migrated"] != float64(1) {
		t.Fatalf("report JSON images_migrated = %v: %s", decoded["images_migrated"], raw)
	}
	if _, present := decoded["images_dropped"]; present {
		t.Fatalf("report JSON should omit images_dropped when nothing was dropped: %s", raw)
	}
}

// TestImportMigratesFileAttachments covers DSH file objects: text files become
// text attachments, binary files become file attachments (metadata + stored
// copy), and the rows are linked to their messages like images.
func TestImportMigratesFileAttachments(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "session-files", "session.v4.jsonl", []string{
		headerLine(t, "session-files", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{
			{"type": "text", "text": "see attachments"},
			dshFilePart(t, home, []byte("# release notes\nfixes\n"), "notes.md"),
			dshFilePart(t, home, []byte{0x00, 0x01, 0x02, 0xff}, "blob.bin"),
		}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.AttachmentsMigrated != 2 || report.ImagesMigrated != 0 {
		t.Fatalf("migrated = %d file / %d image, want 2/0 (report %+v)",
			report.AttachmentsMigrated, report.ImagesMigrated, report)
	}
	sess := singleImportedSession(t, st, report)
	messages, err := st.ListMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := st.ListAttachments(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments = %d, want 2: %+v", len(attachments), attachments)
	}
	byName := map[string]store.Attachment{}
	for _, a := range attachments {
		byName[a.Name] = a
		if a.MessageID != messages[0].ID {
			t.Fatalf("attachment %q message id = %q, want %q", a.Name, a.MessageID, messages[0].ID)
		}
	}
	notes, ok := byName["notes.md"]
	if !ok || notes.Kind != store.AttachmentKindText || notes.MIME != "text/markdown" {
		t.Fatalf("notes.md attachment = %+v (present %v), want text/markdown", notes, ok)
	}
	blob, ok := byName["blob.bin"]
	if !ok || blob.Kind != store.AttachmentKindFile {
		t.Fatalf("blob.bin attachment = %+v (present %v), want kind file", blob, ok)
	}
	data, err := st.ReadAttachmentData(blob, 0)
	if err != nil || len(data) != 4 {
		t.Fatalf("blob.bin stored bytes = %d (err %v), want 4", len(data), err)
	}
}

// TestImportAttachmentSourceProblemsAreSkippedNotFailed pins the failure
// isolation contract: a missing object, an oversized object, a corrupt digest
// and a bare image part are all counted as skips (with granular reasons) while
// the rest of the session still imports.
func TestImportAttachmentSourceProblemsAreSkippedNotFailed(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	good := dshImagePart(t, home, pngBytes, "good.png", "image/png", 1, 1)

	// Missing object: the reference is valid but the DSH object is absent.
	missingHex := digestHex([]byte("missing-object"))
	missing := map[string]any{"type": "image", "attachment": map[string]any{
		"attachmentId": "sha256:" + missingHex, "mediaType": "image/png", "bytes": 10, "name": "missing.png",
	}}

	// Oversized object: a sparse file above the per-file cap; the size is
	// checked before reading so no 25 MiB needs to be materialized.
	oversizedData := []byte("oversized")
	oversizedHex := digestHex(oversizedData)
	oversizedDir := filepath.Join(home, "attachments", "v1", "objects", oversizedHex[:2])
	if err := os.MkdirAll(oversizedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := map[string]any{"type": "image", "attachment": map[string]any{
		"attachmentId": "sha256:" + oversizedHex, "mediaType": "image/png", "bytes": maxAttachmentBytes + 1, "name": "huge.png",
	}}
	big, err := os.Create(filepath.Join(oversizedDir, oversizedHex))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(maxAttachmentBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}

	// Corrupt object: bytes stored under a digest name they do not hash to.
	corruptHex := digestHex([]byte("expected bytes"))
	corruptDir := filepath.Join(home, "attachments", "v1", "objects", corruptHex[:2])
	if err := os.MkdirAll(corruptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, corruptHex), []byte("different bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := map[string]any{"type": "image", "attachment": map[string]any{
		"attachmentId": "sha256:" + corruptHex, "mediaType": "image/png", "bytes": 15, "name": "corrupt.png",
	}}

	// A bare image part carries no resolvable source at all.
	bare := map[string]any{"type": "image"}

	writeFixtureSession(t, home, "--w--", "session-problems", "session.v4.jsonl", []string{
		headerLine(t, "session-problems", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{{"type": "text", "text": "first"}, good}),
		userMessagePartsEvent(t, 2, []map[string]any{missing, oversized, corrupt, bare}),
		assistantMessageEvent(t, 3, []map[string]any{{"type": "text", "text": "done"}}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import must not fail for bad attachment sources: %v", err)
	}
	if report.SessionsImported != 1 || report.SessionsFailed != 0 {
		t.Fatalf("report = %+v, want the session imported despite bad attachments", report)
	}
	if report.ImagesMigrated != 1 || report.ImagesDropped != 4 {
		t.Fatalf("images migrated/dropped = %d/%d, want 1/4 (report %+v)",
			report.ImagesMigrated, report.ImagesDropped, report)
	}
	wantSkips := map[string]int{
		SkipImagesDropped:  4,
		SkipImageMissing:   1,
		SkipImageOversized: 1,
		SkipImageCorrupt:   2, // digest mismatch + bare unresolvable part
	}
	for reason, want := range wantSkips {
		if got := report.Skips[reason]; got != want {
			t.Errorf("Skips[%s] = %d, want %d (all skips %v)", reason, got, want, report.Skips)
		}
	}
	sess := singleImportedSession(t, st, report)
	attachments, err := st.ListAttachments(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Name != "good.png" {
		t.Fatalf("attachments = %+v, want only good.png", attachments)
	}
	messages, err := st.ListMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The image-only user message keeps its (empty) text row so the attachment
	// chip has an owning message.
	if len(messages) != 3 || messages[1].Role != "user" || messages[1].Content != "" {
		t.Fatalf("messages = %+v, want an image-only user message row", messages)
	}
}

// TestImportMigratesInlineBase64Image covers sessions/exports that embed image
// bytes as base64 instead of citing a durable DSH object, plus the corrupt
// payload path (counted, not fatal).
func TestImportMigratesInlineBase64Image(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	legacy := map[string]any{
		"type": "image",
		"source": map[string]any{
			"type": "base64", "media_type": "image/png", "data": encoded, "name": "inline.png",
		},
	}
	wire := map[string]any{"type": "image", "mediaType": "image/png", "data": encoded, "name": "wire.png"}
	broken := map[string]any{
		"type":   "image",
		"source": map[string]any{"type": "base64", "media_type": "image/png", "data": "!!!not-base64!!!"},
	}
	inlineFile := map[string]any{
		"type": "file",
		"attachment": map[string]any{
			"data": base64.StdEncoding.EncodeToString([]byte("inline text file")), "name": "inline.txt",
		},
	}
	writeFixtureSession(t, home, "--w--", "session-inline", "session.v4.jsonl", []string{
		headerLine(t, "session-inline", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{{"type": "text", "text": "inline images"}, legacy, wire, broken, inlineFile}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.ImagesMigrated != 2 || report.ImagesDropped != 1 || report.AttachmentsMigrated != 1 {
		t.Fatalf("migrated = %d image / %d file, dropped %d; want 2/1/1 (report %+v)",
			report.ImagesMigrated, report.AttachmentsMigrated, report.ImagesDropped, report)
	}
	if report.Skips[SkipImageCorrupt] != 1 {
		t.Fatalf("Skips[image_corrupt] = %d, want 1 (%v)", report.Skips[SkipImageCorrupt], report.Skips)
	}
	sess := singleImportedSession(t, st, report)
	attachments, err := st.ListAttachments(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]store.Attachment{}
	for _, a := range attachments {
		names[a.Name] = a
	}
	for _, want := range []string{"inline.png", "wire.png", "inline.txt"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("attachment %q missing (have %v)", want, names)
		}
	}
	if got := names["inline.png"].Kind; got != store.AttachmentKindImage {
		t.Fatalf("inline.png kind = %q, want image", got)
	}
	if got := names["inline.txt"].Kind; got != store.AttachmentKindText {
		t.Fatalf("inline.txt kind = %q, want text", got)
	}
	data, err := st.ReadAttachmentData(names["inline.png"], 0)
	if err != nil || string(data) != string(pngBytes) {
		t.Fatalf("inline image bytes not preserved (len %d, err %v)", len(data), err)
	}
}

// TestImportAttachmentLegacyUnversionedObjectRoot covers DSH builds/backups
// whose attachment objects are not under the versioned v1 root.
func TestImportAttachmentLegacyUnversionedObjectRoot(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	hexID := digestHex(pngBytes)
	dir := filepath.Join(home, "attachments", "objects", hexID[:2])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hexID), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	part := map[string]any{"type": "image", "attachment": map[string]any{
		"attachmentId": "sha256:" + hexID, "mediaType": "image/png", "bytes": len(pngBytes), "name": "legacy.png",
	}}
	writeFixtureSession(t, home, "--w--", "session-legacy-obj", "session.v4.jsonl", []string{
		headerLine(t, "session-legacy-obj", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{{"type": "text", "text": "legacy object"}, part}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.ImagesMigrated != 1 {
		t.Fatalf("ImagesMigrated = %d, want 1 (report %+v)", report.ImagesMigrated, report)
	}
}

// TestImportAttachmentDryRunCountsWithoutWriting proves --dry-run reports the
// migration/skip counts accurately without creating attachment rows or files.
func TestImportAttachmentDryRunCountsWithoutWriting(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "session-dry-att", "session.v4.jsonl", []string{
		headerLine(t, "session-dry-att", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{
			{"type": "text", "text": "dry"},
			dshImagePart(t, home, pngBytes, "dry.png", "image/png", 1, 1),
			map[string]any{"type": "image"},
		}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, DryRun: true})
	if err != nil {
		t.Fatalf("Import dry run: %v", err)
	}
	if report.ImagesMigrated != 1 || report.ImagesDropped != 1 || report.Skips[SkipImageCorrupt] != 1 {
		t.Fatalf("dry-run counts = migrated %d dropped %d skips %v", report.ImagesMigrated, report.ImagesDropped, report.Skips)
	}
	sessions, err := st.QuerySessions(store.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("dry run created %d sessions", len(sessions))
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "attachments")); !os.IsNotExist(err) {
		t.Fatalf("dry run created the attachments directory (err %v)", err)
	}
}

// TestImportAttachmentIdempotentManifest makes sure a repeated run does not
// duplicate attachment rows or files.
func TestImportAttachmentIdempotentManifest(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "session-idem-att", "session.v4.jsonl", []string{
		headerLine(t, "session-idem-att", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{
			{"type": "text", "text": "once"},
			dshImagePart(t, home, pngBytes, "once.png", "image/png", 1, 1),
		}),
	})
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	st := newStore(t)
	opts := Options{DSHHome: home, Store: st, ManifestPath: manifestPath}
	first, err := Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.ImagesMigrated != 1 {
		t.Fatalf("first ImagesMigrated = %d, want 1", first.ImagesMigrated)
	}
	second, err := Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.SessionsImported != 0 || second.Skips[SkipIdempotent] != 1 || second.ImagesMigrated != 0 {
		t.Fatalf("second report = %+v, want an idempotent skip with no new attachments", second)
	}
	sessions, err := st.QuerySessions(store.SessionQuery{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %d (err %v), want 1", len(sessions), err)
	}
	attachments, err := st.ListAttachments(sessions[0].ID)
	if err != nil || len(attachments) != 1 {
		t.Fatalf("attachments = %d (err %v), want 1", len(attachments), err)
	}
}

// TestSanitizeAttachmentFileName pins the path-safety property: a hostile DSH
// display name can never escape the ZeoKeel attachment directory.
func TestSanitizeAttachmentFileName(t *testing.T) {
	cases := map[string]string{
		"diagram.png":            "diagram.png",
		"../../etc/passwd":       "passwd",
		`..\..\windows\evil.exe`: "evil.exe",
		"":                       "attachment",
		"...":                    "attachment",
		"dir/sub/evil.txt":       "evil.txt",
		"a b/c d.txt":            "c_d.txt",
	}
	for in, want := range cases {
		if got := sanitizeAttachmentFileName(in, "attachment"); got != want {
			t.Errorf("sanitizeAttachmentFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeAttachmentFileName("", "image.png"); got != "image.png" {
		t.Errorf("empty image name fallback = %q, want image.png", got)
	}
}

// TestImportAttachmentSymlinkSourceIsRejected: a DSH attachment object that is
// a symlink (to a file inside or outside the DSH home) must not be followed.
// Only a regular file is a valid content-addressed object; the part is counted
// as dropped instead of migrating the link target's bytes.
func TestImportAttachmentSymlinkSourceIsRejected(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	hexID, objectPath := writeDSHObject(t, home, "objects", pngBytes)
	target := filepath.Join(t.TempDir(), "outside-payload.bin")
	if err := os.WriteFile(target, pngBytes, 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Remove(objectPath); err != nil {
		t.Fatalf("remove object: %v", err)
	}
	if err := os.Symlink(target, objectPath); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	sessionFile := writeFixtureSession(t, home, "--data-proj--", "session-symlink", "session.v4.jsonl", []string{
		headerLine(t, "session-symlink", cwd, "user", 0),
		userMessagePartsEvent(t, 1, []map[string]any{
			{"type": "text", "text": "linked diagram"},
			{"type": "image", "attachment": map[string]any{
				"attachmentId": "sha256:" + hexID,
				"mediaType":    "image/png",
				"bytes":        len(pngBytes),
				"name":         "diagram.png",
			}},
		}),
	})
	_ = sessionFile
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.ImagesMigrated != 0 || report.ImagesDropped != 1 {
		t.Fatalf("symlink source migrated/dropped = %d/%d, want 0/1: skips=%+v", report.ImagesMigrated, report.ImagesDropped, report.Skips)
	}
	if report.Skips[SkipImageMissing] != 1 && report.Skips[SkipImageCorrupt] != 1 {
		t.Fatalf("symlink skip reason = %+v, want image_missing or image_corrupt", report.Skips)
	}
	sess := singleImportedSession(t, st, report)
	attachments, err := st.ListAttachments(sess.ID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("symlink target was migrated as an attachment: %+v", attachments)
	}
}
