// Package contracts is ZeoKeel's only shared surface between the host and its
// modules. It holds service keys, host service interfaces, and the value types
// a client contributes.
//
// It is engine-agnostic: it must not import github.com/egoist/mygo, so any
// transport (the gateway's WS/SSE surface included) can depend on it without
// dragging the native UI engine in. The mygo-bound contribution model (screens,
// slots, settings pages, tool renderers, commands and the UI runtime) retired
// with the native UI (S5) and no longer exists in this package tree; what is
// left is the transport-neutral surface the browser client consumes. A module
// may import contracts, design and lib/*, never another module or the host.
package contracts

import (
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ToolCall is the read-only summary a tool renderer receives. The full tool
// state belongs to the session read model and is added when that module is
// ported; keep renderers tolerant of zero values.
// ToolCallFile is one file of a multi-file tool call.
type ToolCallFile struct {
	// Path is the workspace-relative path the call touched.
	Path string
	// Added reports a created file; Deleted reports a removed one (both false
	// means modified/moved).
	Added   bool
	Deleted bool
}

type ToolCall struct {
	ID      string
	Name    string
	Status  string
	Summary string
	// Arguments is the raw JSON arguments of the call (empty when the provider
	// reported none); ACP tool notifications and diagnostics render it.
	Arguments string
	// Path is the workspace-relative path for file-producing tools (write_file,
	// edit_file, str_replace_editor, ...), captured from the full arguments
	// before they are bounded in the transcript. Empty for non-file tools and
	// restored/malformed calls; the generated-file cards use it as the primary
	// source and fall back to parsing Arguments for older records.
	Path string
	// Generated lists every file a multi-file call produced (apply_patch), with
	// its kind, captured from the FULL arguments before they are bounded in the
	// transcript: a bounded patch body is no longer valid JSON and could not be
	// parsed for the cards afterwards. Empty for single-file tools (Path is the
	// source) and for records written before this field existed.
	Generated []ToolCallFile
	// TextOffset is the assistant text length (in bytes) when the call was
	// requested. The transcript renders text/tool cards in this order instead
	// of drawing the whole reply above every call: 0 with text present means
	// "before the text" (restored history without the metadata).
	TextOffset int
	// StartedAt is when the agent requested the call; the zero time means
	// unknown (e.g. restored from a durable event without a timestamp). The UI
	// never fills a zero clock with a guess.
	StartedAt time.Time
	// EndedAt is when the result arrived; the zero time means unknown (running
	// or restored from an event without a timestamp).
	EndedAt time.Time
	// ErrorKind classifies a failed call (Status "error") for localized UI copy
	// using the MessageErrorKind* values ("" = no classification). It is set
	// from the result text, so a rig OS-sandbox attribution line surfaces as
	// MessageErrorKindSandboxDenied on the tool card (2026-11 audit RG-03).
	ErrorKind string `json:"errorKind,omitempty"`
}

// AppInfo is the build identity provided by the appinfo module.
type AppInfo struct {
	Name      string
	Version   string
	Commit    string
	BuildTime string
	GoVersion string
}

// The native UI shell (uiroot slots, navigator, theme, screen list) is gone with
// the desktop product: the browser transport owns every UI service now. Its keys
// live in the files this package is split into (toastanchor.go, foreground.go).

// Service keys: the name is the stable contract, the type is checked by the
// kernel at Provide/Require time and by module.Validate at assembly preflight.
var (
	// KeyLocale is provided by the browser transport host (gateway.Provide).
	KeyLocale = cordis.NewServiceKey[LocaleService]("zeokeel/ui/locale")
	// KeySettings is provided by the process host (internal/host).
	KeySettings = cordis.NewServiceKey[SettingsService]("zeokeel/settings")
	// KeyAppInfo is provided by the appinfo backend module.
	KeyAppInfo = cordis.NewServiceKey[AppInfo]("zeokeel/appinfo")
	// The concrete Rig tool registry (*tools.Registry) is provided by the
	// toolkit module under internal/hosttools.KeyRegistry — a backend seam,
	// so contracts stays free of the tool framework (2026-11 audit AR-06).
	// Consumers that only need tool names/count read KeyToolCatalog
	// (toolcatalog.go) instead.
	// KeyLogger is provided by the process host; modules log through it.
	KeyLogger = cordis.NewServiceKey[Logger]("zeokeel/log")
)

// ThemeService is the host theme switch (System/Light/Dark lives in settings).
type ThemeService interface {
	Dark() bool
	SetDark(dark bool)
	Toggle() bool
}

// LocaleService resolves UI copy. Keys are stable IDs like "app.name".
type LocaleService interface {
	Language() string
	SetLanguage(lang string)
	T(key string) string
}

// Language reports the effective language code of a locale service, defaulting
// to "zh" when none is provided ("zh" when nil).
//
// It lives here rather than in a UI package because backend modules localize
// their own notification copy (the approvals module's waiting/timeout texts) and
// must not depend on the retired native UI layer.
func Language(locale LocaleService) string {
	if locale == nil {
		return "zh"
	}
	return locale.Language()
}

// SettingsService is the host settings store (file-backed in the process host).
type SettingsService interface {
	Get(key, def string) string
	Set(key, value string) error
	All() map[string]string
}

// SettingsDeleter is the optional deletion half of a settings store: cleanup
// paths use it to remove a row instead of persisting an empty value. Callers
// must fall back to Set(key, "") when the store does not implement it.
type SettingsDeleter interface {
	Delete(key string) error
}

// Logger is the host log sink exposed to modules.
type Logger interface {
	Printf(format string, args ...any)
}

// ScreenLister exposes the registered screen IDs in render order.
