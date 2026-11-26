package contracts

// Outbound-only pin for the demoted channels (product decision: ZeoKeel is a
// personal assistant, so a colleague @-ing a bot in a company chat must not be
// able to push a message into the agent that holds this machine's data; see
// docs/research/channel-capabilities.md §6).
//
// DingTalk and Feishu keep the sender surface only. This file fails the build's
// test gate if an inbound method, an ingress constant or a signature/verifier
// interface is re-added — a compiler alone would not notice, because *adding* a
// method to an interface (and to its adapter) compiles fine.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Compile-time half: a sender with only Send + Status satisfies both services.
// If either interface grows an inbound method, these two lines stop compiling.
var (
	_ DingTalkService = dingtalkOutboundStub{}
	_ FeishuService   = feishuOutboundStub{}
)

type dingtalkOutboundStub struct{}

func (dingtalkOutboundStub) Send(context.Context, string, string) error { return nil }
func (dingtalkOutboundStub) Status(context.Context) (DingTalkStatus, error) {
	return DingTalkStatus{}, nil
}

type feishuOutboundStub struct{}

func (feishuOutboundStub) Send(context.Context, string, string) error { return nil }
func (feishuOutboundStub) Status(context.Context) (FeishuStatus, error) {
	return FeishuStatus{}, nil
}

// TestChannelServicesAreOutboundOnly freezes the method set of both channel
// services to exactly Send + Status. Anything else — Receive, VerifyURL,
// CardAction, CardActionWithToken — is an ingress surface.
func TestChannelServicesAreOutboundOnly(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"DingTalkService", reflect.TypeOf((*DingTalkService)(nil)).Elem()},
		{"FeishuService", reflect.TypeOf((*FeishuService)(nil)).Elem()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.typ.Kind() != reflect.Interface {
				t.Fatalf("%s is a %s, want an interface", testCase.name, testCase.typ.Kind())
			}
			var methods []string
			for index := 0; index < testCase.typ.NumMethod(); index++ {
				methods = append(methods, testCase.typ.Method(index).Name)
			}
			sort.Strings(methods)
			if want := []string{"Send", "Status"}; !reflect.DeepEqual(methods, want) {
				t.Fatalf("%s methods = %v, want exactly %v: these channels are outbound only", testCase.name, methods, want)
			}

			// Send takes (ctx, conversation, text) — no headers, no raw body.
			send, ok := testCase.typ.MethodByName("Send")
			if !ok {
				t.Fatal("Send is missing")
			}
			if got, want := send.Type.NumIn(), 3; got != want {
				t.Fatalf("Send takes %d params, want %d (ctx, conversation, text)", got, want)
			}
			for index := 1; index < send.Type.NumIn(); index++ {
				if got := send.Type.In(index).Kind(); got != reflect.String {
					t.Fatalf("Send param %d is %s, want a string", index, got)
				}
			}

			// The status the settings page reads must not carry inbound history:
			// LastInbound was removed together with the ingest path.
			status, ok := testCase.typ.MethodByName("Status")
			if !ok {
				t.Fatal("Status is missing")
			}
			statusType := status.Type.Out(0)
			for index := 0; index < statusType.NumField(); index++ {
				if name := statusType.Field(index).Name; strings.EqualFold(name, "lastInbound") {
					t.Fatalf("%s.%s still reports inbound activity", statusType, name)
				}
			}
		})
	}
}

// TestNoChannelIngressSurfaceSurvives scans every non-test source file in this
// package for a removed declaration. Comments may still name the history; a real
// declaration would resurrect the ingress surface, so it fails here.
func TestNoChannelIngressSurfaceSurvives(t *testing.T) {
	removed := map[string]string{
		"DingTalkMaxSkew":               "the DingTalk callback replay window",
		"ErrDingTalkBadSignature":       "DingTalk callback signature checking",
		"ErrDingTalkStaleTimestamp":     "DingTalk callback replay refusal",
		"ErrDingTalkUnsupportedMessage": "DingTalk callback message parsing",
		"FeishuSource":                  "the Feishu inbox source value",
		"FeishuMaxClockSkew":            "the Feishu callback replay window",
		"FeishuEventMessageReceive":     "the Feishu event type we used to ingest",
		"FeishuDecisionAllow":           "Feishu card decisions",
		"FeishuDecisionDeny":            "Feishu card decisions",
		"FeishuRequestVerifier":         "Feishu callback authentication",
		"FeishuCardActionReader":        "Feishu card action parsing",
		"ErrFeishuBadSignature":         "Feishu callback signature checking",
		"ErrFeishuStale":                "Feishu callback replay refusal",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read contracts dir: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		checked++
		for _, decl := range file.Decls {
			for _, declared := range declaredNames(decl) {
				if why, ok := removed[declared]; ok {
					t.Errorf("%s declares %q again (%s): DingTalk and Feishu are outbound only", name, declared, why)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no contracts source files parsed")
	}
}

// declaredNames lists the top-level names one declaration introduces.
func declaredNames(decl ast.Decl) []string {
	var names []string
	switch typed := decl.(type) {
	case *ast.GenDecl:
		for _, spec := range typed.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, spec.Name.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
			}
		}
	case *ast.FuncDecl:
		if typed.Recv == nil {
			names = append(names, typed.Name.Name)
		}
	}
	return names
}
