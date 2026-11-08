package settings

// Deterministic unit tests for the connection check's success detail: the
// latency and the discovered model names are composed by helpers (not by the
// async UI seam), so these assertions pin the exact formatting. The frame-level
// integration (render, i18n, async flow) lives in model_test.go.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// modelCheckLocaleStub is a fixed-language contracts.LocaleService.
type modelCheckLocaleStub struct{ lang string }

func (l modelCheckLocaleStub) Language() string     { return l.lang }
func (l modelCheckLocaleStub) SetLanguage(_ string) {}
func (l modelCheckLocaleStub) T(key string) string  { return key }

var _ contracts.LocaleService = modelCheckLocaleStub{}

var (
	modelCheckZH = modelCheckLocaleStub{lang: "zh"}
	modelCheckEN = modelCheckLocaleStub{lang: "en"}
)

func TestParseModelList(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantNames []string
		wantKnown bool
	}{
		{"openai shape", `{"data":[{"id":"a"},{"id":"b"}]}`, []string{"a", "b"}, true},
		{"empty data", `{"data":[]}`, []string{}, true},
		{"blank ids skipped", `{"data":[{"id":"a"},{"id":"  "},{"id":" b "}]}`, []string{"a", "b"}, true},
		{"empty body", "", nil, false},
		{"not the openai shape", `{"models":["a"]}`, nil, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			names, known := parseModelList([]byte(test.body))
			if known != test.wantKnown {
				t.Fatalf("known = %v, want %v", known, test.wantKnown)
			}
			if len(names) != len(test.wantNames) {
				t.Fatalf("names = %v, want %v", names, test.wantNames)
			}
			for i := range names {
				if names[i] != test.wantNames[i] {
					t.Fatalf("names = %v, want %v", names, test.wantNames)
				}
			}
		})
	}
}

func TestFormatModelCheckDuration(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{0, "0 ms"},
		{1 * time.Millisecond, "1 ms"},
		{812 * time.Millisecond, "812 ms"},
		{999 * time.Millisecond, "999 ms"},
		{time.Second, "1.0 s"},
		{1234 * time.Millisecond, "1.2 s"},
		{5 * time.Second, "5.0 s"},
	}
	for _, test := range cases {
		if got := formatModelCheckDuration(test.elapsed); got != test.want {
			t.Errorf("formatModelCheckDuration(%v) = %q, want %q", test.elapsed, got, test.want)
		}
	}
}

func TestModelCheckDetailLabel(t *testing.T) {
	cases := []struct {
		name    string
		locale  contracts.LocaleService
		count   int
		names   []string
		known   bool
		elapsed time.Duration
		want    string
	}{
		{
			name: "zh model list and latency", locale: modelCheckZH,
			count: 1, names: []string{"qwen3.8-flash-next"}, known: true, elapsed: 812 * time.Millisecond,
			want: "1 个模型：qwen3.8-flash-next · 812 ms",
		},
		{
			name: "en model list and latency", locale: modelCheckEN,
			count: 1, names: []string{"qwen3.8-flash-next"}, known: true, elapsed: 812 * time.Millisecond,
			want: "1 model: qwen3.8-flash-next · 812 ms",
		},
		{
			name: "zh list bounded to two ids", locale: modelCheckZH,
			count: 4, names: []string{"model-a", "model-b", "model-c", "model-d"}, known: true, elapsed: 40 * time.Millisecond,
			want: "4 个模型：model-a、model-b… · 40 ms",
		},
		{
			name: "en list bounded to two ids", locale: modelCheckEN,
			count: 3, names: []string{"model-a", "model-b", "model-c"}, known: true, elapsed: 40 * time.Millisecond,
			want: "3 models: model-a, model-b… · 40 ms",
		},
		{
			name: "long id is cut", locale: modelCheckZH,
			count: 1, names: []string{"very-long-model-name-that-exceeds-the-cap"}, known: true, elapsed: 1200 * time.Millisecond,
			want: "1 个模型：very-long-model-name-tha… · 1.2 s",
		},
		{
			name: "known count without ids", locale: modelCheckZH,
			count: 0, names: nil, known: true, elapsed: 25 * time.Millisecond,
			want: "0 个模型 · 25 ms",
		},
		{
			name: "unknown body shows latency only", locale: modelCheckEN,
			count: 0, names: nil, known: false, elapsed: 25 * time.Millisecond,
			want: "25 ms",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := modelCheckDetailLabel(test.locale, test.count, test.names, test.known, test.elapsed)
			if got != test.want {
				t.Fatalf("detail = %q, want %q", got, test.want)
			}
		})
	}
}
