package session

// The Todo dock (DSH todo_write alignment).
//
// The agent keeps its plan with the model-facing todo_write tool, whose whole
// list replaces the previous one per call. The toolkit keeps that store and
// exposes it as the optional contracts.TodoService (contracts.KeyTodo); this
// dock renders a light snapshot above the composer, the way DSH shows the
// session's todo list:
//
//   - a collapsible header with the title and the completed/total count;
//   - one row per item with a status marker (hollow = pending, filled = in
//     progress, check = completed), the in-progress item highlighted;
//   - the agent's own list order (US-11): the list is a numbered plan, so
//     status grouping must not shuffle the steps' ordinals — status stays
//     visible through each row's marker and accessible name;
//   - one quiet line disclosing the store's global scope (US-11);
//   - nothing at all when the list is empty or the host provides no service.
//
// SCOPE: the current store is process-level (one list for the whole product),
// not per-session — a todo_write in one conversation is what every
// conversation's dock shows. The dock says so in one line instead of pretending
// to scope it per session. The list is read-only here: the agent owns it, and
// the UI offers no editing or drag-and-drop.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// todoDockMaxHeight bounds the dock's scroll area so a long list never pushes
// the composer off screen; it is six 32 DIP rows.
var todoDockMaxHeight = design.Space8 * 6

// todoService returns the host's optional todo read model (the toolkit's
// todo_write store), or nil when the host does not provide one.
func (st *state) todoService() contracts.TodoService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyTodo)
	if !ok || raw == nil {
		return nil
	}
	todo, _ := raw.(contracts.TodoService)
	return todo
}

// buildTodoDock draws the conversation's todo list above the composer. It
// returns false (drawing nothing) without the optional service or on an empty
// snapshot: an empty dock is chrome, not information.
func buildTodoDock(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) bool {
	if st == nil {
		return false
	}
	service := st.todoService()
	if service == nil {
		return false
	}
	// The agent's order is the plan's order (US-11): render it as written, so
	// numbered steps keep their sequence; the status markers carry the grouping
	// information instead of a re-sort.
	todos := service.Todos()
	if len(todos) == 0 {
		return false
	}
	completed := todoCompletedCount(todos)
	dock := ui.Column(c.Key("session.todo")).FillWidth().Gap(design.Space1)
	// The disclosure state lives on the dock's element: the dock only exists
	// while the list has items, and a cleared list starts expanded again.
	open := ui.Local(dock, "session.todo.open", func() bool { return true })
	dock.Children(func() {
		end := ui.CollapsibleBase(c.Key("session.todo.toggle"), open)
		title := st.tr("待办", "To-do")
		count := fmt.Sprintf("%d/%d", completed, len(todos))
		headerLabel := fmt.Sprintf(st.tr("待办清单，已完成 %d/%d", "To-do list, %d of %d done"),
			completed, len(todos))
		trigger := end.Trigger.FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			PaddingY(design.Space1).Radius(design.RadiusSM).Label(headerLabel)
		if end.Trigger.Hovered() || end.Trigger.Focused() {
			trigger = trigger.Background(t.BgHover)
		}
		trigger.Children(func() {
			// The marker column keeps the same 16 DIP slot as the transcript's
			// fold header, with the disclosure chevron in it.
			ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
				ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
					TextColor(t.LabelTertiary).Rotate(90 * end.Progress())
			})
			design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).
				FontWeight(design.WeightMedium).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, count), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		})
		if *open {
			// The store is process-level: say so once in quiet copy, so a list
			// written in another conversation is not read as this one's plan
			// (US-11). A sentence, so it uses the metadata step (TypeXXS).
			design.Apply(ui.Text(c, st.tr(
				"待办为全应用共享，不区分当前会话。",
				"To-dos are shared across the whole app, not scoped to this conversation.")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
			list := ui.Local(dock, "session.todo.list", func() ui.ListState { return ui.ListState{} })
			list.Key = func(index int) any {
				if index < 0 || index >= len(todos) {
					return nil
				}
				return todos[index].ID
			}
			list.Label = func(index int) string {
				if index < 0 || index >= len(todos) {
					return ""
				}
				return todoStatusLabel(st, todos[index].Status) + " · " + todos[index].Content
			}
			ui.List(c, list, len(todos), func(index int) {
				if index < 0 || index >= len(todos) {
					return
				}
				buildTodoRow(c, t, st, todos[index])
			}).Label(st.tr("待办项", "To-do items")).
				FillWidth().MaxHeight(todoDockMaxHeight)
		}
		if end.Trigger.Changed() {
			// The disclosure already flipped *open; a frame makes the rows
			// follow it.
			runtime.Invalidate()
		}
	})
	return true
}

// buildTodoRow draws one todo item: its status marker, the content and the
// in-progress highlight. It is read-only (no checkbox clicks or dragging): the
// agent replaces the whole list.
func buildTodoRow(c *ui.Context, t *design.Tokens, st *state, todo contracts.TodoItem) {
	status := normalizeTodoStatus(todo.Status)
	inProgress := status == contracts.TodoStatusInProgress
	completed := status == contracts.TodoStatusCompleted
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space1, design.Space2).Radius(design.RadiusSM)
	if inProgress {
		row = row.Background(t.BgSelected)
	}
	row.Children(func() {
		ui.Row(c).Width(design.Space3).Shrink(0).Justify(ui.Center).Children(func() {
			switch {
			case completed:
				ui.Icon(c, icons.Check()).Size(design.Space3, design.Space3).
					TextColor(t.SuccessText)
			case inProgress:
				ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
					Background(t.Warning)
			default:
				ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
					Border(1, t.BorderL3)
			}
		})
		content := design.Apply(ui.Text(c, todo.Content), t.Font(design.TypeXS)).
			MaxLines(1).Ellipsis("…")
		switch {
		case completed:
			content.TextColor(t.LabelTertiary)
		case inProgress:
			content.FontWeight(design.WeightMedium).TextColor(t.LabelPrimary)
		default:
			content.TextColor(t.LabelSecondary)
		}
	})
}

// todoCompletedCount is the header count's numerator (the denominator is the
// whole list): completed/total, as DSH's task panel shows it.
func todoCompletedCount(todos []contracts.TodoItem) int {
	completed := 0
	for _, todo := range todos {
		if normalizeTodoStatus(todo.Status) == contracts.TodoStatusCompleted {
			completed++
		}
	}
	return completed
}

// normalizeTodoStatus maps a stored status onto the contract vocabulary (an
// unknown spelling falls back to pending, as the model-facing tool does).
func normalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case contracts.TodoStatusInProgress:
		return contracts.TodoStatusInProgress
	case contracts.TodoStatusCompleted:
		return contracts.TodoStatusCompleted
	default:
		return contracts.TodoStatusPending
	}
}

// todoStatusLabel is the localized status word of one item (visible in the
// header counts and part of each row's accessible name).
func todoStatusLabel(st *state, status string) string {
	switch normalizeTodoStatus(status) {
	case contracts.TodoStatusInProgress:
		return st.tr("进行中", "In progress")
	case contracts.TodoStatusCompleted:
		return st.tr("已完成", "Completed")
	default:
		return st.tr("待处理", "Pending")
	}
}
