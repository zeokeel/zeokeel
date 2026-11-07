package contracts

// Network settings applied by the process host to outbound HTTP clients and
// child processes (a restart applies changes).
const (
	// SettingNetProxy is an http(s) proxy URL (empty disables the explicit
	// proxy and leaves the environment untouched).
	SettingNetProxy = "net.proxy"
	// SettingNetNoProxy is a comma-separated NO_PROXY list.
	SettingNetNoProxy = "net.noProxy"
)
