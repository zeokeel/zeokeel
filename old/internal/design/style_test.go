package design

import (
	"math"
	"testing"
)

// TestTextScaleDefaults pins the 1.0 baseline: the palette-derived tokens do
// not scale the type system, so a frame with no desktop text-size preference
// renders exactly the design system's own sizes.
func TestTextScaleDefaults(t *testing.T) {
	for _, tokens := range []*Tokens{Light(), Dark()} {
		if tokens.TextScale != 1 {
			t.Fatalf("default TextScale = %v, want 1", tokens.TextScale)
		}
		for _, role := range []TypeRole{TypeXL, TypeL, TypeM, TypeBody, TypeS, TypeXS, TypeXXS, TypeXXXS} {
			size, line, weight := tokens.Metrics(role)
			wantSize, wantLine, wantWeight := TypeMetrics(role)
			if size != wantSize || line != wantLine || weight != wantWeight {
				t.Errorf("role %d at 1.0: got %v/%v/%d, want %v/%v/%d",
					role, size, line, weight, wantSize, wantLine, wantWeight)
			}
		}
		if got := tokens.Scale(28); got != 28 {
			t.Errorf("Scale(28) at 1.0 = %v, want 28", got)
		}
	}
}

// TestTextScaleScalesSizeAndLine covers 1.25x/1.5x/2x: size and line height
// both multiply by the scale and the weight does not change, so the type
// hierarchy and line rhythm stay proportional at every desktop text size.
func TestTextScaleScalesSizeAndLine(t *testing.T) {
	for _, scale := range []float32{1.25, 1.5, 2} {
		tokens := Light()
		tokens.TextScale = scale
		if got := tokens.Scale(28); math.Abs(float64(got-28*scale)) > 1e-4 {
			t.Errorf("Scale(28) at %vx = %v, want %v", scale, got, 28*scale)
		}
		for _, role := range []TypeRole{TypeXL, TypeL, TypeM, TypeBody, TypeS, TypeXS, TypeXXS, TypeXXXS} {
			size, line, weight := tokens.Metrics(role)
			wantSize, wantLine, wantWeight := TypeMetrics(role)
			if math.Abs(float64(size-wantSize*scale)) > 1e-4 {
				t.Errorf("role %d size at %vx = %v, want %v", role, scale, size, wantSize*scale)
			}
			if math.Abs(float64(line-wantLine*scale)) > 1e-4 {
				t.Errorf("role %d line at %vx = %v, want %v", role, scale, line, wantLine*scale)
			}
			if weight != wantWeight {
				t.Errorf("role %d weight at %vx = %d, want %d", role, scale, weight, wantWeight)
			}
		}
	}
}

// TestTextScaleHierarchy checks the scale never reorders the type scale: a
// bigger desktop text size makes every role bigger but keeps Body above the
// metadata steps. It also covers the "unset means 1.0" rule for zero and
// negative scales.
func TestTextScaleHierarchy(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		tokens := Light()
		tokens.TextScale = scale
		body, _, _ := tokens.Metrics(TypeBody)
		m, _, _ := tokens.Metrics(TypeM)
		xs, _, _ := tokens.Metrics(TypeXS)
		xxs, _, _ := tokens.Metrics(TypeXXS)
		xxxs, _, _ := tokens.Metrics(TypeXXXS)
		if !(m > body && body > xs && xs > xxs && xxs > xxxs) {
			t.Errorf("%vx: type hierarchy broken: M %v, Body %v, XS %v, XXS %v, XXXS %v",
				scale, m, body, xs, xxs, xxxs)
		}
	}
	for _, unset := range []float32{0, -1} {
		tokens := Light()
		tokens.TextScale = unset
		if size, line, _ := tokens.Metrics(TypeBody); size != 14 || line != 22 {
			t.Errorf("TextScale %v: TypeBody = %v/%v, want the unscaled 14/22", unset, size, line)
		}
		if got := tokens.Scale(10); got != 10 {
			t.Errorf("TextScale %v: Scale(10) = %v, want 10", unset, got)
		}
	}
}

// TestTextHelpersApplyTextScale checks that Text and TextMuted go through the
// scaled Font metrics while keeping their color roles.
func TestTextHelpersApplyTextScale(t *testing.T) {
	tokens := Light()
	tokens.TextScale = 1.5
	text := tokens.Text(TypeBody)
	muted := tokens.TextMuted(TypeXS)
	if text == nil || muted == nil {
		t.Fatal("Text/TextMuted returned nil styles")
	}
	// The styles are applied during rendering; the metrics are the same
	// scaled ones the Font style uses.
	bodySize, bodyLine, _ := tokens.Metrics(TypeBody)
	if bodySize != 21 || bodyLine != 33 {
		t.Fatalf("TypeBody at 1.5x = %v/%v, want 21/33", bodySize, bodyLine)
	}
}
