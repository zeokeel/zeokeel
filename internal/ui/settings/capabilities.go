package settings

// The 能力 / Capabilities settings page: the limits the agent's delegation and
// the shell tool actually read, in one place. Everything here is a stored
// settings row (no runtime plugin):
//
//   - Subagent concurrency and Team member concurrency (1–8): the agent reads
//     them on every delegation, so a change applies to the next delegation
//     without a restart;
//   - Shell command timeout (5–600s) and Shell output cap (0 = the tool default,
//     or 1–4096 KiB): the toolkit resolves them when the module starts, so a
//     change applies after a restart (the page says so).
//
// The agent loop's parallel-tool cap is hardcoded in rig (8); the page states
// that instead of offering a setting that would not do anything.
//
// The page keeps UI-local float drafts (NumberInput's type), normalizes them
// against the contracts bounds on load/save, and cleans rows that equal the
// product default back to "" so the modules keep following their own config.

import (
	"math"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// CapabilitiesPageTitle is the section title used when the limits half renders
// standalone; the merged Behavior & limits page hides the extra page header.
const CapabilitiesPageTitle = "能力"

// Product defaults the page shows for an unset (clean) row. They match the
// modules' own constants, so a clean save never changes the effective limits.
const (
	capabilitiesDefaultSubagentConcurrent  = 2
	capabilitiesDefaultTeamConcurrent      = 4
	capabilitiesDefaultShellTimeoutSeconds = 30
	capabilitiesDefaultShellOutputKiB      = 0
	// capabilitiesDefaultApprovalTimeoutSeconds is 0 = disabled (the default):
	// interactive approvals keep waiting; only automation-origin runs may opt
	// into a per-approval auto-deny (2026-11 audit RG-04, personal-scale ruling).
	capabilitiesDefaultApprovalTimeoutSeconds = 0
)

// capabilitiesState is the page's UI-local draft/status state.
type capabilitiesState struct {
	loaded bool
	// Drafts are float64 because ui.NumberInput binds that type; the integer
	// value is rounded and clamped through the accessors below.
	subagentMax         float64
	teamMax             float64
	shellTimeoutSeconds float64
	shellOutputKiB      float64
	approvalTimeout     float64
	status              string
	statusErr           bool
}

// newCapabilitiesState returns the page with its product-default drafts. The
// host asks every page whether it is dirty before the page is built, so the
// drafts must start at the defaults (matching an unset registry) instead of
// zero values the pages would have to clamp.
func newCapabilitiesState() *capabilitiesState {
	return &capabilitiesState{
		subagentMax:         capabilitiesDefaultSubagentConcurrent,
		teamMax:             capabilitiesDefaultTeamConcurrent,
		shellTimeoutSeconds: capabilitiesDefaultShellTimeoutSeconds,
		shellOutputKiB:      capabilitiesDefaultShellOutputKiB,
		approvalTimeout:     capabilitiesDefaultApprovalTimeoutSeconds,
	}
}

// capabilitiesRound rounds a NumberInput draft to an int inside [lo, hi].
func capabilitiesRound(value float64, lo, hi int) int {
	rounded := int(math.Round(value))
	if rounded < lo {
		return lo
	}
	if rounded > hi {
		return hi
	}
	return rounded
}

// draft accessors: the four limits as the integers the setting rows store.
func (st *capabilitiesState) subagentValue() int {
	return capabilitiesRound(st.subagentMax, contracts.CapabilityMinConcurrency, contracts.CapabilityMaxConcurrency)
}

func (st *capabilitiesState) teamValue() int {
	return capabilitiesRound(st.teamMax, contracts.CapabilityMinConcurrency, contracts.CapabilityMaxConcurrency)
}

func (st *capabilitiesState) timeoutValue() int {
	return capabilitiesRound(st.shellTimeoutSeconds,
		contracts.CapabilityMinShellTimeoutSeconds, contracts.CapabilityMaxShellTimeoutSeconds)
}

func (st *capabilitiesState) outputValue() int {
	return capabilitiesRound(st.shellOutputKiB,
		contracts.CapabilityShellOutputDefaultKiB, contracts.CapabilityMaxShellOutputKiB)
}

func (st *capabilitiesState) approvalTimeoutValue() int {
	return capabilitiesRound(st.approvalTimeout, 0, contracts.CapabilityMaxApprovalTimeoutSeconds)
}

// capabilitiesStored reads one row the same way the modules do: an absent or
// invalid row falls back to the product default.
func capabilitiesStored(store contracts.SettingsService, key string, fallback int) int {
	if store == nil {
		return fallback
	}
	raw := store.Get(key, "")
	switch key {
	case contracts.SettingAgentSubagentMaxConcurrent, contracts.SettingAgentTeamMaxConcurrent:
		return contracts.NormalizeCapabilityConcurrency(raw, fallback)
	case contracts.SettingToolkitShellTimeoutSeconds:
		return contracts.NormalizeShellTimeoutSeconds(raw, fallback)
	case contracts.SettingToolkitShellMaxOutputKiB:
		return contracts.NormalizeShellMaxOutputKiB(raw, fallback)
	case contracts.SettingAgentApprovalTimeoutSeconds:
		return contracts.NormalizeApprovalTimeoutSeconds(raw)
	}
	return fallback
}

// load (re-)reads the persisted rows into the drafts and clears the status.
func (st *capabilitiesState) load(store contracts.SettingsService) {
	st.loaded = true
	subagent := capabilitiesDefaultSubagentConcurrent
	team := capabilitiesDefaultTeamConcurrent
	timeout := capabilitiesDefaultShellTimeoutSeconds
	output := capabilitiesDefaultShellOutputKiB
	approval := capabilitiesDefaultApprovalTimeoutSeconds
	if store != nil {
		subagent = capabilitiesStored(store, contracts.SettingAgentSubagentMaxConcurrent, subagent)
		team = capabilitiesStored(store, contracts.SettingAgentTeamMaxConcurrent, team)
		timeout = capabilitiesStored(store, contracts.SettingToolkitShellTimeoutSeconds, timeout)
		output = capabilitiesStored(store, contracts.SettingToolkitShellMaxOutputKiB, output)
		approval = capabilitiesStored(store, contracts.SettingAgentApprovalTimeoutSeconds, approval)
	}
	st.subagentMax = float64(subagent)
	st.teamMax = float64(team)
	st.shellTimeoutSeconds = float64(timeout)
	st.shellOutputKiB = float64(output)
	st.approvalTimeout = float64(approval)
	st.status = ""
	st.statusErr = false
}

// capabilitiesRows describes the stored rows in page order.
func capabilitiesRows(st *capabilitiesState) []struct {
	key   string
	value int
	clean int
} {
	return []struct {
		key   string
		value int
		clean int
	}{
		{contracts.SettingAgentSubagentMaxConcurrent, st.subagentValue(), capabilitiesDefaultSubagentConcurrent},
		{contracts.SettingAgentTeamMaxConcurrent, st.teamValue(), capabilitiesDefaultTeamConcurrent},
		{contracts.SettingToolkitShellTimeoutSeconds, st.timeoutValue(), capabilitiesDefaultShellTimeoutSeconds},
		{contracts.SettingToolkitShellMaxOutputKiB, st.outputValue(), capabilitiesDefaultShellOutputKiB},
		{contracts.SettingAgentApprovalTimeoutSeconds, st.approvalTimeoutValue(), capabilitiesDefaultApprovalTimeoutSeconds},
	}
}

// dirty reports whether the drafts differ from the effective stored rows. An
// unopened page has no drafts to lose, so it is never dirty.
func (st *capabilitiesState) dirty(store contracts.SettingsService) bool {
	if !st.loaded {
		return false
	}
	for _, row := range capabilitiesRows(st) {
		if row.value != capabilitiesStored(store, row.key, row.clean) {
			return true
		}
	}
	return false
}

// save validates the drafts once more (defensive: NumberInput already keeps them
// in range) and writes the four rows. A value equal to the product default is
// cleaned to an empty row, so the modules follow their own config again instead
// of a redundant copy of the default.
func (st *capabilitiesState) save(store contracts.SettingsService, locale contracts.LocaleService) {
	if store == nil {
		st.status = localized(locale, "设置服务不可用", "The settings service is unavailable")
		st.statusErr = true
		return
	}
	for _, row := range capabilitiesRows(st) {
		value := strconv.Itoa(row.value)
		if row.value == row.clean {
			value = ""
		}
		if err := store.Set(row.key, value); err != nil {
			st.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
			st.statusErr = true
			return
		}
	}
	st.status = localized(locale, "已保存", "Saved")
	st.statusErr = false
}

// restoreDefaults resets the drafts to the product defaults, unsaved: the user
// reviews them and presses Save to clean the rows.
func (st *capabilitiesState) restoreDefaults(locale contracts.LocaleService) {
	st.loaded = true
	st.subagentMax = capabilitiesDefaultSubagentConcurrent
	st.teamMax = capabilitiesDefaultTeamConcurrent
	st.shellTimeoutSeconds = capabilitiesDefaultShellTimeoutSeconds
	st.shellOutputKiB = capabilitiesDefaultShellOutputKiB
	st.approvalTimeout = capabilitiesDefaultApprovalTimeoutSeconds
	st.status = localized(locale, "已恢复默认（未保存）", "Defaults restored (unsaved)")
	st.statusErr = false
}

// discardDrafts drops the drafts and re-reads the rows, so reopening after
// "Discard" shows the saved limits.
func (st *capabilitiesState) discardDrafts(store contracts.SettingsService) {
	st.load(store)
}

// buildCapabilitiesPage renders the page body.
func buildCapabilitiesPage(c *ui.Context, store contracts.SettingsService,
	st *capabilitiesState, locale contracts.LocaleService, showHeader bool) {
	t := design.Of(c)
	if !st.loaded {
		st.load(store)
	}
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		if showHeader {
			uikit.PageHeader(c, localized(locale, CapabilitiesPageTitle, "Capabilities"),
				localized(locale, "委派并发与 Shell 命令的运行上限。",
					"Run limits for delegation concurrency and shell commands."))
		}

		uikit.Section(c, localized(locale, "委派并发", "Delegation concurrency"), func(body *ui.Context) {
			ui.Field(body, localized(locale, "Subagent 并发", "Subagent concurrency"), func() {
				ui.NumberInput(body, &st.subagentMax,
					contracts.CapabilityMinConcurrency, contracts.CapabilityMaxConcurrency, 1).
					Label(inputLabel(locale, localized(locale, "Subagent 并发", "Subagent concurrency")))
			}).Description(localized(locale,
				"一次 Subagent 委派同时运行的最多子代理数（1–8）。每次委派读取，修改后下一次委派生效。",
				"Live children of one subagent delegation (1–8). Read per delegation; a change applies to the next one."))
			ui.Field(body, localized(locale, "Team member 并发", "Team member concurrency"), func() {
				ui.NumberInput(body, &st.teamMax,
					contracts.CapabilityMinConcurrency, contracts.CapabilityMaxConcurrency, 1).
					Label(inputLabel(locale, localized(locale, "Team member 并发", "Team member concurrency")))
			}).Description(localized(locale,
				"一个 Team member 同时运行的最多子代理数（1–8）。每次启动读取，修改后下一次生效。",
				"Live subagents of one team member (1–8). Read per start; a change applies to the next one."))
		})

		uikit.Section(c, localized(locale, "Shell 命令", "Shell commands"), func(body *ui.Context) {
			ui.Field(body, localized(locale, "Shell 命令超时", "Shell command timeout"), func() {
				ui.NumberInput(body, &st.shellTimeoutSeconds,
					contracts.CapabilityMinShellTimeoutSeconds, contracts.CapabilityMaxShellTimeoutSeconds, 5).
					Label(inputLabel(locale, localized(locale, "Shell 命令超时", "Shell command timeout")))
			}).Description(localized(locale,
				"单条 Shell 命令的最长运行秒数（5–600）。修改后重启应用生效。",
				"Longest runtime of one shell command in seconds (5–600). Restart the app to apply."))
			ui.Field(body, localized(locale, "Shell 输出上限", "Shell output cap"), func() {
				ui.NumberInput(body, &st.shellOutputKiB,
					contracts.CapabilityShellOutputDefaultKiB, contracts.CapabilityMaxShellOutputKiB, 64).
					Label(inputLabel(locale, localized(locale, "Shell 输出上限", "Shell output cap")))
			}).Description(localized(locale,
				"单条命令返回内容的上限（KiB，最大 4096）；0 表示使用工具默认。修改后重启应用生效。",
				"Output cap of one command in KiB (at most 4096); 0 uses the tool default. Restart the app to apply."))
		})

		uikit.Section(c, localized(locale, "审批超时", "Approval timeout"), func(body *ui.Context) {
			ui.Field(body, localized(locale, "审批超时", "Approval timeout"), func() {
				ui.NumberInput(body, &st.approvalTimeout,
					0, contracts.CapabilityMaxApprovalTimeoutSeconds, 30).
					Label(inputLabel(locale, localized(locale, "审批超时", "Approval timeout")))
			}).Description(localized(locale,
				"仅自动化来源的审批等待生效：超过该秒数自动拒绝并记录、通知。0＝关闭（默认），交互审批不受影响；运行级墙钟断路器仍生效。修改后下次运行生效。",
				"Only approvals from automation-origin runs are bounded: after this many seconds they are auto-denied, recorded and notified. 0 = disabled (default); interactive approvals are unaffected and the run-level wall-clock breaker still applies. A change applies on the next run."))
		})

		uikit.Section(c, localized(locale, "并行工具上限", "Parallel tool calls"), func(body *ui.Context) {
			// The agent loop's parallel-tool cap is a rig constant: say so
			// instead of offering a setting that would do nothing.
			design.Apply(ui.Text(body, localized(locale,
				"工具并行上限由 rig 固定为 8，当前不可配置。",
				"The rig caps parallel tool calls per round at 8; this is not configurable.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})

		statusBadgesRow(c, t, st.status, st.statusErr)
	})
}

// capabilitiesPageKeywords feeds the settings search with this page's terms.
func capabilitiesPageKeywords() []string {
	return []string{
		"能力", "capabilities", "capability",
		"subagent", "子代理", "委派", "delegation", "并发", "concurrency", "parallel",
		"team", "团队", "member", "成员",
		"shell", "命令", "command", "超时", "timeout", "输出", "output", "上限", "limit", "kib",
		"审批", "approval", "自动化", "automation", "拒绝", "deny",
	}
}
