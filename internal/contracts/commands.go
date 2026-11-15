package contracts

// Command palette categories: the host groups the palette's rows under one
// heading per category, in this order (actions, then navigation, then
// settings). CommandSpec.Category is optional and the empty value is the
// default actions group, so the command surface that existed before grouping
// keeps working unchanged.
const (
	// CommandCategoryActions is the default group: state-changing actions the
	// window or a module contributes (new session, toggle the inspector, ...).
	CommandCategoryActions = "actions"
	// CommandCategoryGoTo is the navigation group. The host generates one
	// command per visible screen ("前往 自动化"), so a palette user reaches the
	// product's destinations without hunting the sidebar.
	CommandCategoryGoTo = "goto"
	// CommandCategorySettings is the settings group. The host generates one
	// command per visible settings page ("设置：外观"), a deep link to the page
	// through the same OpenSettingsPage path modules use.
	CommandCategorySettings = "settings"
)
