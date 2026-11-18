package settings

// The shared page anatomy of the settings pages (B-spec-v2 §3.4): the control
// column widths, the description line and the section rhythm. They live here so
// the daily pages cannot drift onto a different measure: a text field is 320
// wide in every page, a number field 160, and an explanation under a control is
// 12/18 tertiary.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

const (
	// controlWidth is the form control column: text inputs, selects and the
	// segmented controls that sit beside them (B-spec §3.4.4).
	controlWidth float32 = 320
	// numberControlWidth is the narrower column of a numeric field, so a run
	// limit does not read as a wide free-text box (B-spec §3.4.4).
	numberControlWidth float32 = 160
)

// descStyle is the page's description line: 12/18 tertiary under the control it
// explains (B-spec §3.4.2). It is the one description vocabulary of the pages —
// t.TextMuted is the 13/20 secondary one and is kept for inline summaries.
func descStyle(t *design.Tokens) design.Style {
	return func(el ui.Element) ui.Element {
		return design.Apply(el, t.Font(design.TypeXXS), func(e ui.Element) ui.Element {
			return e.TextColor(t.LabelTertiary)
		})
	}
}

// pageSectionGap is the space between two page regions (B-spec §3.4.2: section
// titles carry 24 above, 8 below; the page column itself uses the screen
// rhythm).
const pageSectionGap float32 = design.SpaceScreen
