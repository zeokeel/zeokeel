package conform

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
)

// hexColorRe is the color-literal shape of DESIGN-LINT §3.1: a string that is
// nothing but a CSS hex color.
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// colorConstructors are the mygo ui functions that construct a color from
// literal components (DESIGN-LINT §3.1).
var colorConstructors = map[string]bool{
	"RGB":  true,
	"RGBA": true,
	"Hex":  true,
}

// checkDS01 reports color literals in UI code: direct color constructors and
// hex color string literals. The token implementation packages are excluded by
// the path scope in scan.go.
func checkDS01(fc *fileCtx) []Violation {
	rule := ruleByID("DS-01")
	var out []Violation

	// First pass: constructor calls. Their arguments are skipped in the
	// literal pass so ui.Hex("#333") is one violation, not two.
	type span struct{ from, to int }
	var spans []span
	ast.Inspect(fc.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := fc.callName(call, mygoUIPath)
		if !ok || !colorConstructors[name] {
			return true
		}
		text := fc.snippet(call.Pos(), call.End())
		v := fc.violation(rule, rule.Severity, call.Pos(), text,
			"颜色字面量："+text,
			"用 design.Of(c) / c.Theme() 的语义色；阴影用 t.ShadowOverlay()/t.ShadowComposer()（DESIGN.md §5.1 / AP-01）")
		out = append(out, v)
		spans = append(spans, span{fc.fset.Position(call.Pos()).Offset, fc.fset.Position(call.End()).Offset})
		return true
	})

	// Second pass: bare hex strings ("#333") anywhere else in UI code.
	ast.Inspect(fc.file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		raw, err := strconv.Unquote(lit.Value)
		if err != nil || !hexColorRe.MatchString(raw) {
			return true
		}
		off := fc.fset.Position(lit.Pos()).Offset
		for _, s := range spans {
			if off >= s.from && off < s.to {
				return true // already reported as part of the constructor call
			}
		}
		v := fc.violation(rule, rule.Severity, lit.Pos(), lit.Value,
			"颜色字面量："+lit.Value,
			"用 design.Of(c) / c.Theme() 的语义色（DESIGN.md §5.1 / AP-01）")
		out = append(out, v)
		return true
	})
	return out
}

// metricMethods is the monitored method set of DESIGN-LINT §3.3: a numeric
// literal argument to any of these is a raw metric.
var metricMethods = map[string]bool{
	"FontSize": true, "FontWeight": true,
	"Padding": true, "PaddingX": true, "PaddingY": true,
	"PaddingStart": true, "PaddingEnd": true, "PaddingTop": true, "PaddingBottom": true,
	"Gap": true, "GapX": true, "GapY": true,
	"Radius": true, "Height": true, "Width": true,
	"MinHeight": true, "MaxHeight": true, "MinWidth": true, "MaxWidth": true,
	"Size": true, "Shadow": true,
}

// metricFix gives the actionable replacement for a monitored method.
func metricFix(method string) string {
	switch method {
	case "FontSize":
		return "用 t.Font(design.Type*) / design.Apply(el, t.Font(...)) 的字号 role；mono 小字待 design.TypeMono（DESIGN.md D6 / §6.3）"
	case "FontWeight":
		return "标题字重尚未 role 化（DESIGN.md D6）：优先复用 t.Font(design.Type*)；新增字重需 token 化后再收紧为 error"
	case "Font":
		return "用 design.Apply(el, t.Font(design.Type*))"
	case "Padding", "PaddingX", "PaddingY", "PaddingStart", "PaddingEnd", "PaddingTop", "PaddingBottom":
		return "用 design.Space1–Space8（4 DIP 网格；DESIGN.md §5.4）"
	case "Gap", "GapX", "GapY":
		return "用 design.Space1–Space8（4 DIP 网格；DESIGN.md §5.4）"
	case "Radius":
		return "用 design.RadiusXS/SM/MD/LG/XL/Max（DESIGN.md §5.4）"
	case "Height", "Width", "MinHeight", "MaxHeight", "MinWidth", "MaxWidth":
		return "用 design.Space*/design.DialogWidthS/M/L/design.*Width 等布局 token；新尺寸先加 token（DESIGN.md D6）"
	case "Size":
		return "图标/控件尺寸用 design token 或等价的 uikit.IconSize/ButtonHeight；对话框宽度用 design.DialogWidthS/M/L"
	case "Shadow":
		return "用 t.ShadowOverlay()/t.ShadowComposer()（design 包唯一的阴影来源；深色 alpha 由 token 处理）"
	}
	return "改用 design token（DESIGN.md §5.4 / AP-03）"
}

// checkDS03 reports raw numeric metric literals (DESIGN-LINT §3.3). Bare
// FontWeight literals are a warn: heading weights have no type role yet
// (DESIGN.md D6), so they are grandfathered instead of blocking.
func checkDS03(fc *fileCtx) []Violation {
	rule := ruleByID("DS-03")
	var out []Violation
	ast.Inspect(fc.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := sel.Sel.Name
		if !metricMethods[method] {
			return true
		}
		var literal *ast.BasicLit
		for _, arg := range call.Args {
			if lit := numericLiteral(arg); lit != nil {
				literal = lit
				break
			}
		}
		if literal == nil {
			return true
		}
		text := fc.snippet(sel.Sel.Pos(), call.End())
		severity := rule.Severity
		prefix := "裸数值度量：" + text
		if method == "FontWeight" {
			severity = SeverityWarn
			prefix = "裸字重：" + text
		}
		out = append(out, fc.violation(rule, severity, literal.Pos(), text, prefix, metricFix(method)))
		return true
	})
	return out
}

// numericLiteral unwraps parentheses and unary signs to find a numeric
// literal argument.
func numericLiteral(expr ast.Expr) *ast.BasicLit {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT || e.Kind == token.FLOAT {
			return e
		}
	case *ast.ParenExpr:
		return numericLiteral(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.SUB || e.Op == token.ADD {
			return numericLiteral(e.X)
		}
	}
	return nil
}

// selfDrawnBases are the mygo *Base constructors that reconstruct an official
// widget's interaction (DESIGN-LINT §3.6). Each call site must be registered
// in the registry allowlist or it is a DS-06 violation (AP-04).
var selfDrawnBases = map[string]bool{
	"ButtonBase": true,
	"DialogBase": true,
}

// findAllow returns the first allowlist entry covering file for a rule.
func findAllow(allow []AllowEntry, rule, file string) (AllowEntry, bool) {
	for _, a := range allow {
		rules := a.Rules
		if len(rules) == 0 {
			rules = []string{"DS-06"}
		}
		entry := a
		entry.Rules = rules
		if entry.matches(rule, file) {
			return entry, true
		}
	}
	return AllowEntry{}, false
}

// checkDS06 reports unregistered self-drawn base calls. Registered sites stay
// visible as a warn until the entry component (uikit.IconButton) converges
// them.
func checkDS06(fc *fileCtx, allow []AllowEntry) []Violation {
	rule := ruleByID("DS-06")
	var out []Violation
	ast.Inspect(fc.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := fc.callName(call, mygoUIPath)
		if !ok || !selfDrawnBases[name] {
			return true
		}
		text := fc.snippet(call.Pos(), call.End())
		v := fc.violation(rule, rule.Severity, call.Pos(), text,
			"未登记自绘："+name+"（mygo *Base 重建了官方交互，必须登记 allowlist 并保留 hover/focus/pressed 语义）",
			"优先用官方件或 uikit 入口（待建 uikit.IconButton）；确需自绘则同步 DESIGN.md §7.1 + internal/design/registry.json allowlist（owner/reason/until）")
		if entry, ok := findAllow(allow, rule.ID, fc.rel); ok {
			v.Allowed = true
			v.AllowID = entry.ID
			v.Owner = entry.Owner
			v.Expires = entry.Until
			v.Severity = SeverityWarn
			v.Message = "已登记自绘（待收敛）：" + name + "（owner=" + entry.Owner + " until=" + entry.Until + "）"
		}
		out = append(out, v)
		return true
	})
	return out
}

// ds07EmojiWhitelist are typographic symbols that can look emoji-ish but are
// legitimate glyphs in UI copy (DESIGN-LINT §3.7 excludes such typography).
var ds07EmojiWhitelist = map[rune]bool{
	'✓': true, '✔': true, '✗': true, '✘': true, '✕': true, '✖': true,
	'★': true, '☆': true, '●': true, '○': true, '◆': true, '◇': true,
	'■': true, '□': true, '▶': true, '◀': true, '▲': true, '▼': true,
}

// emojiRune reports the first emoji/Unicode pictograph in s.
func emojiRune(s string) (rune, bool) {
	for _, r := range s {
		if ds07EmojiWhitelist[r] {
			continue
		}
		switch {
		case r >= 0x1F300 && r <= 0x1FAFF:
			return r, true
		case r >= 0x2600 && r <= 0x27BF:
			return r, true
		}
	}
	return 0, false
}

// checkDS07 reports emoji/Unicode icon literals in UI packages and SVG parsing
// outside internal/icons (DESIGN-LINT §3.7 / AP-10).
func checkDS07(fc *fileCtx) []Violation {
	rule := ruleByID("DS-07")
	var out []Violation
	if ds07AppliesEmoji(fc.rel) {
		ast.Inspect(fc.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			raw, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if r, ok := emojiRune(raw); ok {
				v := fc.violation(rule, rule.Severity, lit.Pos(), lit.Value,
					"emoji/Unicode 图标字面量："+strconv.QuoteRune(r)+"（跨平台渲染不一致，禁用 emoji/Unicode 当图标）",
					"改用 internal/icons/svg + ui.Icon(c, icons.X())；图标按钮要有 Label+Tooltip（DESIGN.md §1.2 G / AP-10）")
				out = append(out, v)
			}
			return true
		})
	}
	if ds07AppliesSVG(fc.rel) {
		ast.Inspect(fc.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := fc.callName(call, mygoUIPath)
			if !ok || (name != "ParseSVG" && name != "MustParseSVG") {
				return true
			}
			text := fc.snippet(call.Pos(), call.End())
			v := fc.violation(rule, rule.Severity, call.Pos(), text,
				"手写 SVG："+name+"（图标资产必须集中在 internal/icons/svg）",
				"图标只从 internal/icons 取：新增 SVG 资源 + accessor，视图用 ui.Icon(c, icons.X())（DESIGN.md §1.2 G / AP-10）")
			out = append(out, v)
			return true
		})
	}
	return out
}

// isDialogActionType reports whether expr is (or names) uikit.DialogAction.
func isDialogActionType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		return t.Sel != nil && t.Sel.Name == "DialogAction"
	case *ast.Ident:
		return t.Name == "DialogAction"
	}
	return false
}

// isDialogActionSlice reports whether expr is []uikit.DialogAction (the form
// uikit.DialogFooter takes, whose elements may elide the type).
func isDialogActionSlice(expr ast.Expr) bool {
	arr, ok := expr.(*ast.ArrayType)
	if !ok {
		return false
	}
	return isDialogActionType(arr.Elt)
}

// hasDangerDefault reports the DialogAction{Danger: true, Default: true}
// combination.
func hasDangerDefault(lit *ast.CompositeLit) bool {
	var danger, def bool
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		id, ok := kv.Value.(*ast.Ident)
		if !ok || id.Name != "true" {
			continue
		}
		switch key.Name {
		case "Danger":
			danger = true
		case "Default":
			def = true
		}
	}
	return danger && def
}

// checkDS10 reports a DialogAction that is both Danger and Default: a
// destructive action must never be the Enter default (AP-12, the approval
// incident). Frame tests still own the behavioural half (DESIGN-LINT §3.10).
func checkDS10(fc *fileCtx) []Violation {
	rule := ruleByID("DS-10")
	var out []Violation
	report := func(lit *ast.CompositeLit) {
		if !hasDangerDefault(lit) {
			return
		}
		text := fc.snippet(lit.Pos(), lit.End())
		if text == "" {
			text = "DialogAction{Danger: true, Default: true}"
		}
		out = append(out, fc.violation(rule, rule.Severity, lit.Pos(), text,
			"DialogAction 同时 Danger+Default：危险动作不得成为 Enter 默认（真实误触事故，AP-12）",
			"去掉 Default（危险/审批对话框无 Default；焦点留给取消或输入），见 DESIGN.md §2.1 / uikit.DialogFooter 注释"))
	}
	ast.Inspect(fc.file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if isDialogActionType(lit.Type) {
			report(lit)
			return true
		}
		if isDialogActionSlice(lit.Type) {
			for _, elt := range lit.Elts {
				if el, ok := elt.(*ast.CompositeLit); ok && el.Type == nil {
					report(el)
				}
			}
		}
		return true
	})
	return out
}
