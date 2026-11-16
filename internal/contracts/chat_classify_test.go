package contracts

import "testing"

// RG-03 (2026-11 audit): rig v0.18.1 appends a stable attribution line to
// shell/jobs results that match a known OS-sandbox denial dialect:
//
//	[sandbox: suspected OS sandbox denial (matched "permission denied"; policy mode workspace-write, enforcement confined); …]
//
// ClassifyError must recognize that marker as its own error kind so the UI can
// render a localized "the OS sandbox denied this" hint instead of a generic
// failure, and ordinary failures must keep their existing classification.
func TestClassifyErrorSandboxAttribution(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{
			name: "permission denied dialect",
			msg:  "cp: cannot create regular file '/etc/hosts': Permission denied\n[sandbox: suspected OS sandbox denial (matched \"permission denied\"; policy mode workspace-write, enforcement confined); the command ran inside the OS file sandbox]",
		},
		{
			name: "read-only filesystem dialect",
			msg:  "touch: cannot touch 'x': Read-only file system\n[sandbox: suspected OS sandbox denial (matched \"read-only file system\"; policy mode read-only, enforcement confined)]",
		},
		{
			name: "windows access denied dialect",
			msg:  "Access is denied.\n[sandbox: suspected OS sandbox denial (matched \"access is denied\"; policy mode workspace-write, enforcement confined)]",
		},
		{
			name: "marker alone",
			msg:  "[sandbox: suspected OS sandbox denial (matched \"operation not permitted\"; policy mode workspace-write, enforcement confined)]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(nil, tc.msg); got != MessageErrorKindSandboxDenied {
				t.Fatalf("ClassifyError(%q) = %q, want %q", tc.msg, got, MessageErrorKindSandboxDenied)
			}
		})
	}
}

// Control: the same denial dialects WITHOUT the rig attribution line keep their
// existing classification (the marker is the attribution signal, not a guess
// from bare stderr), and the existing kinds are unchanged.
func TestClassifyErrorOrdinaryFailuresUnchanged(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"cp: cannot create regular file '/etc/hosts': Permission denied", MessageErrorKindOther},
		{"touch: cannot touch 'x': Read-only file system", MessageErrorKindOther},
		{"Access is denied.", MessageErrorKindOther},
		{"operation not permitted", MessageErrorKindOther},
		{"401 Unauthorized", MessageErrorKindAuth},
		{"429 rate limit exceeded", MessageErrorKindRateLimit},
		{"request timed out", MessageErrorKindTimeout},
		{"connection refused", MessageErrorKindNetwork},
		{"maximum context length exceeded", MessageErrorKindContext},
		{"context canceled", MessageErrorKindCancelled},
		{"max iterations: 20 round(s) without a final answer", MessageErrorKindIterations},
	}
	for _, tc := range cases {
		if got := ClassifyError(nil, tc.msg); got != tc.want {
			t.Fatalf("ClassifyError(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

// A cancelled run stays cancelled even when the partial output carries a
// sandbox attribution line: cancellation wins over the attribution.
func TestClassifyErrorCancellationWinsOverSandbox(t *testing.T) {
	msg := "[sandbox: suspected OS sandbox denial (matched \"permission denied\"; policy mode workspace-write, enforcement confined)]\ncontext canceled"
	if got := ClassifyError(nil, msg); got != MessageErrorKindCancelled {
		t.Fatalf("ClassifyError = %q, want %q", got, MessageErrorKindCancelled)
	}
}
