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

	"github.com/zeokeel/zeokeel/old/internal/app"
	"github.com/zeokeel/zeokeel/old/internal/importer"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// importUsage is the `zeokeel import` help text. It deliberately says that
// credential VALUES are never read: the importer only records key names so a
// report/log can be shared safely (PD-04).
const importUsage = `Usage: zeokeel import [options]

Migrate DSH (DeepSeek Harness) sessions into this ZeoKeel data directory.
User/assistant text and reasoning are imported; images and file attachments are
migrated as ZeoKeel attachments linked to their message (tool calls/results and
trajectory are still not imported), and subagent sessions are skipped unless
--include-subagents is given. The DSH data is read, never modified. Credential
key names are listed in the report; values are never read, printed or stored.
Start with --dry-run.

A real import is refused while the ZeoKeel desktop app is running for this data
directory: quit the app first. Pass --force to import anyway (not recommended).

Options:
  --from <dir>          DSH data directory (default: ~/.dsh)
  --dry-run             Analyze and report only; write nothing
  --json                Print one JSON object (the importer report) on stdout
  --max <n>             Import at most n sessions (0 = all)
  --include-subagents   Also import sessions whose header origin is subagent
  --manifest <file>     Idempotency manifest (default: <data dir>/dsh-import-manifest.json)
  --no-manifest         Disable idempotency: re-import every session on every run
  --force               Import even if the desktop app appears to be running
  -h, --help            Show this help

Exit codes: 0 import completed (0 sessions is not an error), 1 app running /
DSH not found / store error / some sessions failed, 2 usage error.
`

// DefaultImportManifestName is the per-data-directory idempotency manifest used
// when --manifest is not given. It records imported DSH sessions (source hash /
// size / mtime) so a repeated import skips unchanged sessions instead of
// duplicating conversations (PD-04 X4).
const DefaultImportManifestName = "dsh-import-manifest.json"

// probeInstanceLock is the seam over app.ProbeInstanceLock: a real import must
// be refused while the desktop app holds the data directory's single-instance
// lock. It is a variable so cmd-level tests can simulate a running app.
var probeInstanceLock = app.ProbeInstanceLock

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
	force            bool
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
		case arg == "--force":
			opts.force = true
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
	// Refuse a real import while the desktop app holds this data directory's
	// single-instance lock (residual PD-04 item): the app may be writing the
	// same database, and the import opens the same store. The lock probe is
	// non-blocking and never keeps the lock. --force is the explicit escape
	// hatch; a probe failure warns instead of blocking (the store's own locking
	// still protects the data).
	if !opts.dryRun {
		running, probeErr := probeInstanceLock(app.DataDir())
		switch {
		case probeErr != nil:
			fmt.Fprintf(stderr, "zeokeel import: warning: cannot check whether ZeoKeel is running in %s: %v\n", app.DataDir(), probeErr)
		case running != nil:
			owner := app.DataDir()
			if running.PID > 0 {
				owner = fmt.Sprintf("%s (pid %d)", owner, running.PID)
			}
			if !opts.force {
				fmt.Fprintf(stderr,
					"zeokeel import: ZeoKeel appears to be running for data directory %s.\nQuit the desktop app before importing, or re-run with --force to import anyway.\n",
					owner)
				return 1
			}
			fmt.Fprintf(stderr, "zeokeel import: warning: ZeoKeel appears to be running for data directory %s; continuing because --force was given.\n", owner)
		}
	}
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
	if r.ImagesMigrated > 0 || r.AttachmentsMigrated > 0 {
		fmt.Fprintf(w, "attachments: %d image(s) and %d file(s) migrated\n", r.ImagesMigrated, r.AttachmentsMigrated)
	}
	if r.ImagesDropped > 0 || r.AttachmentsDropped > 0 {
		fmt.Fprintf(w, "attachments: %d image(s) and %d file(s) skipped (source missing, too large or unreadable)\n",
			r.ImagesDropped, r.AttachmentsDropped)
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
