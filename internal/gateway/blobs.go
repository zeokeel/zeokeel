package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Blob store (TRANSPORT §6.4 / §925).
//
// Attachments travel as blob ids: the run request carries `attachments: [id]`, the
// gateway resolves each id to the bytes it stored at upload time, and the message
// keeps the metadata. Blobs are ephemeral — the message is the durable record — so
// a gateway restart drops them, and a run that outlives its blob is refused loudly
// instead of attaching an empty file.
//
// Ids are 128 random bits rendered as hex: unguessable and unenumerable, because
// GET /api/blobs/{id} is reachable by anything that holds the process token.

const (
	// blobMaxBytes is one attachment's ceiling (10 MiB, aligned with ACP's
	// MaxPromptImageBytes).
	blobMaxBytes = 10 << 20
	// blobTTL bounds how long an uploaded blob waits for its run.
	blobTTL = 30 * time.Minute
)

type blob struct {
	data    []byte
	mime    string
	name    string
	created time.Time
}

type blobStore struct {
	mu    sync.Mutex
	items map[string]blob
	now   func() time.Time
}

func newBlobStore() *blobStore {
	return &blobStore{items: map[string]blob{}, now: time.Now}
}

func (s *blobStore) put(data []byte, mime, name string) (string, error) {
	id, err := newBlobID()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	// Sweep on write, not only on read: an upload whose blob is never read back
	// (the user pasted an image and then did not send) used to stay in memory
	// until the process restarted, because expiry was only checked by get
	// (AUDIT-2026-11-24 B-11).
	s.sweepExpiredLocked()
	s.items[id] = blob{data: data, mime: mime, name: name, created: s.now()}
	s.mu.Unlock()
	return id, nil
}

// sweepExpiredLocked drops every blob past its TTL. Callers hold s.mu.
func (s *blobStore) sweepExpiredLocked() {
	now := s.now()
	for id, item := range s.items {
		if now.Sub(item.created) > blobTTL {
			delete(s.items, id)
		}
	}
}

func (s *blobStore) get(id string) (blob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return blob{}, false
	}
	if s.now().Sub(item.created) > blobTTL {
		delete(s.items, id)
		return blob{}, false
	}
	return item, true
}

func newBlobID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// handleBlobs serves POST /api/blobs (upload) and GET /api/blobs/{id} (fetch).
func (g *Gateway) handleBlobs(w http.ResponseWriter, r *http.Request) {
	if err := g.authorizeRequest(r, true); err != nil {
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/blobs":
		data, err := io.ReadAll(io.LimitReader(r.Body, blobMaxBytes+1))
		if err != nil {
			writeProtocolError(w, http.StatusBadRequest, errInvalidParams("body", "could not be read"))
			return
		}
		if len(data) == 0 {
			writeProtocolError(w, http.StatusBadRequest, errInvalidParams("body", "is empty"))
			return
		}
		if len(data) > blobMaxBytes {
			writeProtocolError(w, http.StatusRequestEntityTooLarge,
				errInvalidParams("body", "exceeds the 10 MiB attachment limit"))
			return
		}
		mime := strings.TrimSpace(r.Header.Get("Content-Type"))
		if mime == "" || mime == "application/octet-stream" {
			mime = http.DetectContentType(data)
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		id, err := g.blobs.put(data, mime, name)
		if err != nil {
			writeProtocolError(w, http.StatusInternalServerError, internalError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "mime": mime, "bytes": len(data), "name": name})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/blobs/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/blobs/")
		item, ok := g.blobs.get(id)
		if !ok {
			writeProtocolError(w, http.StatusNotFound, errInvalidParams("id", "unknown blob"))
			return
		}
		w.Header().Set("Content-Type", item.mime)
		w.Header().Set("Content-Length", strconv.Itoa(len(item.data)))
		w.Header().Set("Cache-Control", "private, max-age=600")
		_, _ = w.Write(item.data)
	default:
		writeProtocolError(w, http.StatusMethodNotAllowed,
			protocolErrorWithMessage(codeMethodNotFound, "method not allowed"))
	}
}

// resolveAttachments turns uploaded blob ids into contract attachments, refusing an
// unknown id instead of silently sending fewer files.
func (g *Gateway) resolveAttachments(ids []string) ([]contracts.Attachment, *protocolError) {
	attachments := make([]contracts.Attachment, 0, len(ids))
	for _, id := range ids {
		item, ok := g.blobs.get(strings.TrimSpace(id))
		if !ok {
			return nil, errInvalidParams("attachments", "unknown or expired blob "+id)
		}
		attachments = append(attachments, contracts.Attachment{
			ID:   id,
			Name: item.name,
			MIME: item.mime,
			Data: item.data,
		})
	}
	return attachments, nil
}
