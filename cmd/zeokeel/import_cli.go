package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zeokeel/zeokeel/internal/app"
	"github.com/zeokeel/zeokeel/internal/importer"
	"github.com/zeokeel/zeokeel/internal/store"
)

// importUsage is the `zeokeel import` help text. It deliberately says that
// credential VALUES are never read: the importer only records key names so a
// report/log can be shared safely (PD-04).
const importUsage = `Usage: zeokeel import [options]

Migrate DSH (DeepSeek Harness) sessions into this ZeoKeel data directory.
This is the minimal import (technical preview): text conversations are
imported; tool calls are counted/summarized, images/attachments/trajectory are
not, and subagent sessions are skipped unless --include-subagents is given.
The DSH data is read, never modified. Credential key names are listed in the
report; values are never read, printed or stored. Start with --dry-run.

Options:
  --from <dir>          DSH data directory (default: ~/.dsh)
  --dry-run             Analyze and report only; write nothing
  --json                Print one JSON object (the importer report) on stdout
  --max <n>             Import at most n sessions (0 = all)
  --include-subagents   Also import sessions whose header origin is subagent
  --manifest <file>     Idempotency manifest (default: <data dir>/dsh-import-manifest.json)
  --no-manifest         Disable idempotency: re-import every session on every run
  -h, --help            Show this help

Exit codes: 0 import completed (0 sessions is not an error), 1 DSH not found /
store error / some sessions failed, 2 usage error.

Quit the desktop app before importing: a running app may be writing to the same
data directory.
`

// DefaultImportManifestName is the per-data-directory idempotency manifest used
// when --manifest is not given. It records imported DSH sessions (source hash /
// size / mtime) so a repeated import skips unchanged sessions instead of
// duplicating conversations (PD-04 X4).
const DefaultImportManifestName = "dsh-import-manifest.json"

// importOptions is the parsed `zeokeel import` command line.
type importOptions struct {
	help             bool
	dryRun           bool
	json             bool
	from             string
	maxSessions      int
	includeSubagents bool
	manifest         string
	noManifest       bool
}

// parseImportArgs parses the import flags. Unknown flags are usage errors so a
// typo can never start a destructive import with default settings.
func parseImportArgs(args []string) (importOptions, error) {
	var opts importOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			opts.help = true
		case arg == "--dry-run":
			opts.dryRun = true
		case arg == "--json":
			opts.json = true
		case arg == "--include-subagents":
			opts.includeSubagents = true
		case arg == "--from":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--from needs a directory")
			}
			i++
			opts.from = args[i]
		case strings.HasPrefix(arg, "--from="):
			opts.from = strings.TrimPrefix(arg, "--from=")
		case arg == "--manifest":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--manifest needs a file path")
			}
			i++
			opts.manifest = args[i]
		case strings.HasPrefix(arg, "--manifest="):
			opts.manifest = strings.TrimPrefix(arg, "--manifest=")
		case arg == "--no-manifest":
			opts.noManifest = true
		case arg == "--max":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--max needs a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return opts, fmt.Errorf("--max needs a non-negative number, got %q", args[i])
			}
			opts.maxSessions = n
		case strings.HasPrefix(arg, "--max="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--max="))
			if err != nil || n < 0 {
				return opts, fmt.Errorf("--max needs a non-negative number, got %q", strings.TrimPrefix(arg, "--max="))
			}
			opts.maxSessions = n
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag %q", arg)
		default:
			return opts, fmt.Errorf("unexpected argument %q (the import command takes flags only)", arg)
		}
	}
	if opts.noManifest && strings.TrimSpace(opts.manifest) != "" {
		return opts, fmt.Errorf("--manifest and --no-manifest are mutually exclusive")
	}
	return opts, nil
}

// runImportCLI implements `zeokeel import` (PD-04): it probes the DSH data
// directory, imports sessions through internal/importer, and reports the
// result. With --dry-run nothing is written and no ZeoKeel store is opened.
func runImportCLI(args []string, stdout, stderr io.Writer) int {
	opts, err := parseImportArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "zeokeel import: %v\n\n%s", err, importUsage)
		return 2
	}
	if opts.help {
		fmt.Fprint(stdout, importUsage)
		return 0
	}

	ctx := context.Background()
	// Idempotency manifest: default to a per-data-directory file so running the
	// command twice does not duplicate sessions (PD-04 X4). --no-manifest keeps
	// the historical re-import-everything behaviour.
	manifestPath := strings.TrimSpace(opts.manifest)
	if opts.noManifest {
		manifestPath = ""
	} else if manifestPath == "" {
		manifestPath = filepath.Join(app.DataDir(), DefaultImportManifestName)
	}
	importOpts := importer.Options{
		DSHHome:                 opts.from,
		DryRun:                  opts.dryRun,
		IncludeSubagentSessions: opts.includeSubagents,
		MaxSessions:             opts.maxSessions,
		ManifestPath:            manifestPath,
	}

	if !opts.dryRun {
		// A real import needs the destination store. Open creates/migrates the
		// data directory, which is the same store the desktop app uses.
		st, err := store.Open(app.DataDir())
		if err != nil {
			fmt.Fprintf(stderr, "zeokeel import: open data directory %s: %v\n", app.DataDir(), err)
			return 1
		}
		defer func() { _ = st.Close() }()
		importOpts.Store = st
	}

	report, err := importer.Import(ctx, importOpts)
	if err != nil {
		if strings.Contains(err.Error(), importer.ErrNotInstalled.Error()) {
			fmt.Fprintf(stderr, "zeokeel import: %v\n\nNo DSH data found. Use --from <dir> to point at a different DSH data directory.\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "zeokeel import: %v\n", err)
		return 1
	}

	if opts.json {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "zeokeel import: encode report: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
	} else {
		if manifestPath == "" {
			fmt.Fprintln(stdout, "manifest: disabled (--no-manifest): every run re-imports all sessions")
		} else {
			fmt.Fprintf(stdout, "manifest: %s (repeated runs skip unchanged sessions)\n", manifestPath)
		}
		printImportReport(stdout, report)
	}
	if len(report.Errors) > 0 {
		return 1
	}
	return 0
}

// printImportReport renders the report as a short human summary. It lists
// credential KEY NAMES only (importer never records values).
func printImportReport(w io.Writer, r *importer.Report) {
	mode := ""
	if r.DryRun {
		mode = " (dry run: nothing written)"
	}
	fmt.Fprintf(w, "DSH home: %s%s\n", r.DSHHome, mode)
	fmt.Fprintf(w, "sessions: found %d, imported %d, skipped %d, failed %d\n",
		r.SessionsFound, r.SessionsImported, r.SessionsSkipped, r.SessionsFailed)
	fmt.Fprintf(w, "messages: %d imported (%d system skipped), tool calls %d / results %d\n",
		r.MessagesImported, r.SystemMessages, r.ToolCallsSeen, r.ToolResultsSeen)
	if r.CompressedInput {
		fmt.Fprintln(w, "input: some sessions were zstd-compressed")
	}
	if r.ImagesDropped > 0 {
		fmt.Fprintf(w, "images: %d dropped (minimal import migrates text conversations only)\n", r.ImagesDropped)
	}
	if len(r.Skips) > 0 {
		keys := make([]string, 0, len(r.Skips))
		for k := range r.Skips {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, r.Skips[k]))
		}
		fmt.Fprintf(w, "skips: %s\n", strings.Join(parts, " "))
	}
	if len(r.Credentials) > 0 {
		names := make([]string, 0, len(r.Credentials))
		for _, ref := range r.Credentials {
			names = append(names, ref.Name)
		}
		fmt.Fprintf(w, "credentials present (values never read): %s\n", strings.Join(names, ", "))
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
	if len(r.Errors) > 0 {
		fmt.Fprintln(w, "errors:")
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  - %s: %s\n", e.SessionFile, e.Error)
		}
	}
}
