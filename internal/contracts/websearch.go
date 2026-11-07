package contracts

// Web search settings. The toolkit builds the rig web_search tool from these
// rows at call time, so a change applies without a restart.
const (
	// SettingWebSearchEndpoint is the absolute http(s) URL of the search
	// backend (empty disables web_search).
	SettingWebSearchEndpoint = "web.search.endpoint"
	// SettingWebSearchAPIKey is sent as "Authorization: Bearer <key>" when set.
	SettingWebSearchAPIKey = "web.search.apiKey"
)
