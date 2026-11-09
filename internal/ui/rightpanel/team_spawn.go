package rightpanel

// State transitions of the "new teammate" dialog. The dialog's visual anatomy
// (ui.Form/Field Modal, no default action) lives in uikit.TeamSurface
// (AGENT-SURFACES.md S9); this module owns the spawn itself, which runs off the
// UI thread (the runner may wait for a concurrency slot) and posts its result
// back through contracts.UIRuntime.

import (
	"context"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// teamSpawnTimeout bounds one spawn attempt from the dialog (the runner may
// block on its concurrency cap; the teammate run itself is unbounded).
const teamSpawnTimeout = 30 * time.Second

// openTeamSpawnDialog resets and opens the native form.
func (st *state) openTeamSpawnDialog() {
	st.teamSpawnOpen = true
	st.teamSpawnName = ""
	st.teamSpawnPrompt = ""
	st.teamSpawnModel = ""
	st.teamSpawnErr = ""
	st.teamSpawnBusy = false
}

// cancelTeamSpawn closes the dialog and cancels an in-flight spawn attempt.
func (st *state) cancelTeamSpawn() {
	if st.teamSpawnCancel != nil {
		st.teamSpawnCancel()
		st.teamSpawnCancel = nil
	}
	st.teamSpawnOpen = false
	st.teamSpawnBusy = false
	st.teamSpawnErr = ""
}

// submitTeamSpawn starts one teammate off the UI thread and posts the result.
func (st *state) submitTeamSpawn(team contracts.TeamService, runtime contracts.UIRuntime) {
	if team == nil || st.teamSpawnBusy || runtime == nil {
		return
	}
	name := strings.TrimSpace(st.teamSpawnName)
	prompt := strings.TrimSpace(st.teamSpawnPrompt)
	model := strings.TrimSpace(st.teamSpawnModel)
	if name == "" || prompt == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), teamSpawnTimeout)
	st.teamSpawnBusy = true
	st.teamSpawnErr = ""
	st.teamSpawnCancel = cancel
	go func() {
		member, err := team.Spawn(ctx, name, prompt, model)
		cancelled := ctx.Err() != nil
		cancel()
		runtime.Post(func() {
			if st.teamSpawnBusy {
				st.teamSpawnBusy = false
			}
			st.teamSpawnCancel = nil
			if cancelled {
				// The user closed the dialog or the attempt timed out; the
				// manager may still have registered the member, but the dialog
				// must not reopen over another screen.
				runtime.Invalidate()
				return
			}
			if err != nil {
				st.teamSpawnErr = st.tr("创建队友失败：", "Failed to create teammate: ") + err.Error()
				runtime.Invalidate()
				return
			}
			st.teamSpawnOpen = false
			st.selectedMemberID = member.ID
			st.selectedSubagentID = ""
			st.teamErr = ""
			st.teamStatus = st.tr("已创建队友", "Teammate created")
			runtime.Invalidate()
		})
	}()
}
