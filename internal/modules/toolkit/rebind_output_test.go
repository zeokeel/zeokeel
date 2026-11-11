package toolkit

// Tests for the central output-repair seam in the root-scoped tool proxy: raw
// Windows console bytes (GBK/cp936, UTF-16LE) must reach the UI and the model as
// valid UTF-8, while ordinary UTF-8 output and the wrapper's semantics stay
// untouched.

import (
	"context"
	"errors"
	"testing"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// stubTool is a deterministic inner rig.Tool for boundTool.Invoke tests.
type stubTool struct {
	result  rig.ToolResult
	err     error
	invoked int
}

func (s *stubTool) Name() string           { return "stub_tool" }
func (s *stubTool) Description() string    { return "stub tool description" }
func (s *stubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (s *stubTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	s.invoked++
	return s.result, s.err
}

// boundStub wires a stub as the only tool of a group whose root never changes,
// so the proxy dispatches straight to it without building real Rig tools.
func boundStub(tool rig.Tool) (*boundTool, *rebindGroup) {
	group := &rebindGroup{
		rootOf: func() string { return "" },
		current: &rootScoped{
			root:   "",
			mode:   sandbox.ModeWorkspaceWrite,
			tools:  []rig.Tool{tool},
			byName: map[string]rig.Tool{tool.Name(): tool},
		},
	}
	return &boundTool{meta: tool, group: group}, group
}

// TestBoundToolRepairsGBKOutput is the core regression for the Chinese-Windows
// mojibake report: a tool that returns raw GBK/cp936 console bytes (as Rig's
// pwsh/terminal tools do on Windows) must hand valid UTF-8 to the caller.
func TestBoundToolRepairsGBKOutput(t *testing.T) {
	want := "构建成功：中文输出"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatalf("encode GBK: %v", err)
	}
	if utf8.Valid(gbk) {
		t.Fatal("test premise broken: GBK bytes are valid UTF-8")
	}

	inner := &stubTool{result: rig.ToolResult{Content: string(gbk)}}
	proxy, _ := boundStub(inner)
	result, invokeErr := proxy.Invoke(context.Background(), rig.ToolCall{Name: inner.Name()})
	if invokeErr != nil {
		t.Fatalf("Invoke: %v", invokeErr)
	}
	if inner.invoked != 1 {
		t.Fatalf("inner tool invoked %d times, want 1", inner.invoked)
	}
	if result.Content != want {
		t.Fatalf("repaired content = %q, want %q", result.Content, want)
	}
	if !utf8.ValidString(result.Content) {
		t.Fatalf("content is not valid UTF-8: %q", result.Content)
	}
}

// TestBoundToolPreservesValidOutputAndSemantics covers the contract the wrapper
// must not change: UTF-8 output passes through byte-for-byte, IsError survives,
// and the inner error is returned as-is (wrapped content still repaired).
func TestBoundToolPreservesValidOutputAndSemantics(t *testing.T) {
	const validUTF8 = "already utf-8 中文 ✅\n"
	innerErr := errors.New("tool exploded")
	gbkFailure, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("失败"))
	if err != nil {
		t.Fatalf("encode GBK: %v", err)
	}

	cases := []struct {
		name        string
		innerResult rig.ToolResult
		innerErr    error
		wantContent string
		wantIsError bool
	}{
		{
			name:        "valid utf8 unchanged",
			innerResult: rig.ToolResult{Content: validUTF8},
			wantContent: validUTF8,
		},
		{
			name:        "valid utf8 with iserror kept",
			innerResult: rig.ToolResult{Content: validUTF8, IsError: true},
			wantContent: validUTF8,
			wantIsError: true,
		},
		{
			name:        "iserror with gbk content repaired",
			innerResult: rig.ToolResult{Content: string(gbkFailure), IsError: true},
			wantContent: "失败",
			wantIsError: true,
		},
		{
			name:        "error returned unchanged with gbk content repaired",
			innerResult: rig.ToolResult{Content: string(gbkFailure)},
			innerErr:    innerErr,
			wantContent: "失败",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			inner := &stubTool{result: testCase.innerResult, err: testCase.innerErr}
			proxy, _ := boundStub(inner)
			result, invokeErr := proxy.Invoke(context.Background(), rig.ToolCall{Name: inner.Name()})
			if testCase.innerErr == nil {
				if invokeErr != nil {
					t.Fatalf("Invoke error = %v, want nil", invokeErr)
				}
			} else if !errors.Is(invokeErr, testCase.innerErr) {
				t.Fatalf("Invoke error = %v, want %v", invokeErr, testCase.innerErr)
			}
			if result.Content != testCase.wantContent {
				t.Fatalf("content = %q, want %q", result.Content, testCase.wantContent)
			}
			if result.IsError != testCase.wantIsError {
				t.Fatalf("IsError = %t, want %t", result.IsError, testCase.wantIsError)
			}
		})
	}
}

// TestBoundToolUnknownToolFailsClosed keeps the not-found branch untouched by
// the output repair.
func TestBoundToolUnknownToolFailsClosed(t *testing.T) {
	group := &rebindGroup{
		rootOf:  func() string { return "" },
		current: &rootScoped{root: "", mode: sandbox.ModeWorkspaceWrite, byName: map[string]rig.Tool{}},
	}
	proxy := &boundTool{meta: &stubTool{}, group: group}
	if _, err := proxy.Invoke(context.Background(), rig.ToolCall{Name: "stub_tool"}); !errors.Is(err, rig.ErrToolNotFound) {
		t.Fatalf("Invoke(missing) error = %v, want rig.ErrToolNotFound", err)
	}
}

// TestBoundToolMetadataPassthrough pins that the registry-visible metadata is
// still the inner tool's (the proxy exists to keep names/schemas stable).
func TestBoundToolMetadataPassthrough(t *testing.T) {
	inner := &stubTool{}
	proxy, _ := boundStub(inner)
	if proxy.Name() != inner.Name() || proxy.Description() != inner.Description() {
		t.Fatalf("proxy metadata = %q/%q, want %q/%q",
			proxy.Name(), proxy.Description(), inner.Name(), inner.Description())
	}
	if got, want := proxy.Schema()["type"], inner.Schema()["type"]; got != want {
		t.Fatalf("proxy schema type = %v, want %v", got, want)
	}
}
