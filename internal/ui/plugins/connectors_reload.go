package plugins

// Connector hot reload: after a settings write the toolkit rebuilds the MCP
// connections and tool registrations in place. The UI asks for that rebuild off
// the main thread and posts the result back, so adding/editing/removing a
// connector applies immediately instead of after an app restart.

import (
	"context"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// requestMCPReload asks the toolkit to apply connector changes. When the
// composition has no reloader (older test seams) it falls back to the previous
// restart-required copy.
func (st *state) requestMCPReload(success string) {
	st.requestMCPReloadOr(success, st.tr("已保存，重启生效", "Saved; restart applies"))
}

func (st *state) requestMCPReloadOr(success, fallback string) {
	if st == nil {
		return
	}
	reloader, ok := st.ctx.Get(contracts.KeyMCPReloader)
	if !ok || reloader == nil {
		st.status = fallback
		st.statusErr = false
		return
	}
	if st.connectors != nil {
		st.connectors.reloading = true
	}
	st.status = st.tr("正在应用连接器变更…", "Applying connector changes…")
	st.statusErr = false
	go func() {
		_, err := reloader.Reload(context.Background())
		st.runtime.Post(func() {
			if st.connectors != nil {
				st.connectors.reloading = false
			}
			if err != nil {
				st.status = st.tr("应用失败：", "Apply failed: ") + err.Error()
				st.statusErr = true
			} else {
				st.status = success
				st.statusErr = false
			}
			st.runtime.Invalidate()
		})
	}()
}

// mcpHotReload reports whether the composition provides a reloader, so status
// copy can say "applies now" instead of "restart applies".
func (st *state) mcpHotReload() bool {
	if st == nil {
		return false
	}
	reloader, ok := st.ctx.Get(contracts.KeyMCPReloader)
	return ok && reloader != nil
}

// mcpReloading reports whether a connector reload is in flight (used by the
// collection row spinner and to keep the reconnect button disabled).
func (st *state) mcpReloading() bool {
	return st != nil && st.connectors != nil && st.connectors.reloading
}
