package home

// Home's real composer: a native multiline input with Enter-to-send, read-only
// workspace/model/mode chips, staged attachments and a primary Send action.
// It creates a fresh session for the first prompt (Home is the new-conversation
// surface), sends through the optional AttachmentSender when available, then
// navigates to the conversation screen.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// AttachmentPicker is the Home composer's file-picker seam; tests replace it
// with a scripted selection so no desktop backend is needed.
var AttachmentPicker = func() ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:    "选择附件",
		Multiple: true,
		Filters: []mygo.FileFilter{{
			Name: "文件与图片",
			Extensions: []string{
				"png", "jpg", "jpeg", "gif", "webp",
				"txt", "md", "json", "yaml", "yml", "go", "log", "csv",
			},
		}},
	})
}

// WorkspaceDirectoryPicker is the Home empty state's "add workspace" seam: the
// default opens the native folder dialog; tests replace it with a scripted
// selection so no desktop backend is needed.
var WorkspaceDirectoryPicker = func() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     "选择工作区文件夹",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

const (
	homeMaxAttachmentBytes = 10 << 20
	homeAttachmentInputCap = 16
)

// homeComposerState is the Home composer's UI-thread state.
type homeComposerState struct {
	draft       string
	attachments []contracts.Attachment
	attachErr   string
	sendErr     string
	notice      string
	sending     bool
	attachSeq   int
	// workspaceErr is the last failed workspace switch/add from the composer's
	// context row; it shows inline below the actions like the other errors.
	workspaceErr string
	// modelErr is the last failed model-level thinking-default write (the model
	// menu edits the catalog row); it shows inline below the actions.
	modelErr string
	// nextStepErr is the empty-state action failure (adding a workspace).
	nextStepErr string
	// attachBusy is true while the native picker runs off the UI thread
	// (mygo's dialog blocks its caller): the attach actions show
	// 选择中…/Choosing… and stay disabled until the posted result stages the
	// files.
	attachBusy bool
	// attachPathsOpen/attachPathDraft/attachPathErr drive the manual path-entry
	// fallback for environments without a working native picker (ZK-09).
	attachPathsOpen bool
	attachPathDraft string
	attachPathErr   string
}

// homeAttachmentMIME maps a picked file to the attachment content type.
func homeAttachmentMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt", ".md", ".json", ".yaml", ".yml", ".go", ".log", ".csv":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// stageHomeAttachments reads the picked paths into staged attachments,
// keeping the first readable error for the composer's danger line.
func stageHomeAttachments(st *homeComposerState, paths []string, tr func(zh, en string) string) {
	st.attachErr = ""
	for _, path := range paths {
		if len(st.attachments) >= homeAttachmentInputCap {
			st.attachErr = tr("附件数量已达上限", "Attachment limit reached")
			break
		}
		name := filepath.Base(path)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			st.attachErr = tr("无法添加 "+name+"：不是可读的普通文件", "Could not add "+name+": not a readable regular file")
			continue
		}
		if info.Size() > homeMaxAttachmentBytes {
			st.attachErr = tr(name+" 超过 10 MB 附件上限", name+" exceeds the 10 MB attachment limit")
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			st.attachErr = tr("无法读取 "+name+"：", "Could not read "+name+": ") + err.Error()
			continue
		}
		st.attachSeq++
		st.attachments = append(st.attachments, contracts.Attachment{
			ID:   fmt.Sprintf("home-att-%d", st.attachSeq),
			Name: name,
			MIME: homeAttachmentMIME(path),
			Data: data,
		})
	}
}

// stageHomePastedImage turns an image on the clipboard into a staged
// attachment, under the same caps and error lines as a picked file. The paste
// hook cannot use the frame's Context (it runs between frames), so the caller
// passes the Services captured while building.
func stageHomePastedImage(st *homeComposerState, services ui.Services, tr func(zh, en string) string) bool {
	if len(st.attachments) >= homeAttachmentInputCap {
		st.attachErr = tr("附件数量已达上限", "Attachment limit reached")
		return true
	}
	data, err := services.ReadClipboardData(transfer.PNG).Read(transfer.PNG)
	if err != nil || len(data) == 0 {
		st.attachErr = tr("剪贴板里没有图片；请用「附件」或拖拽添加图片",
			"No image on the clipboard; use Attach or drag an image in")
		return true
	}
	if len(data) > homeMaxAttachmentBytes {
		st.attachErr = tr("粘贴的图片超过 10 MB 附件上限", "The pasted image exceeds the 10 MB attachment limit")
		return true
	}
	st.attachSeq++
	st.attachments = append(st.attachments, contracts.Attachment{
		ID:   fmt.Sprintf("home-att-%d", st.attachSeq),
		Name: fmt.Sprintf("pasted-image-%d.png", st.attachSeq),
		MIME: "image/png",
		Data: data,
	})
	st.attachErr = ""
	return true
}

// startHomeAttachmentPick opens the native file picker off the UI thread and
// publishes the result through UIRuntime.Post. mygo's Dialog.Open blocks its
// caller, so a synchronous call from a frame would freeze the window; the
// composer shows a busy attach action until the posted result stages the files.
// A second click while busy is ignored (the action is disabled as well).
func startHomeAttachmentPick(runtime contracts.UIRuntime, st *homeComposerState, tr func(zh, en string) string) {
	if st.attachBusy {
		return
	}
	st.attachBusy = true
	st.attachErr = ""
	runtime.Invalidate()
	go func() {
		paths, err := AttachmentPicker()
		runtime.Post(func() {
			st.attachBusy = false
			if err != nil {
				st.attachErr = tr("无法打开文件选择器：", "Could not open the file picker: ") + err.Error()
			} else {
				stageHomeAttachments(st, paths, tr)
			}
			runtime.Invalidate()
		})
	}()
}

// homeManualAttachmentErrors localizes the rejected entries of the manual path
// fallback, one line per entry, so a typo names the exact path.
func homeManualAttachmentErrors(invalid []uikit.InvalidAttachmentPath, tr func(zh, en string) string) []string {
	messages := make([]string, 0, len(invalid))
	for _, entry := range invalid {
		switch {
		case entry.Relative:
			messages = append(messages, tr("路径必须是绝对路径：", "Path must be absolute: ")+entry.Path)
		default:
			messages = append(messages, tr("文件不存在或不可读：", "File does not exist or is not readable: ")+entry.Path)
		}
	}
	return messages
}

// applyHomeManualAttachmentPaths validates and stages one manual path-entry
// submission. Valid paths go through the ordinary staging path; invalid entries
// and staging errors stay inline in the dialog, which therefore does not close
// on a partial failure. It reports whether the dialog should close.
func applyHomeManualAttachmentPaths(st *homeComposerState, raw string, tr func(zh, en string) string) bool {
	if strings.TrimSpace(raw) == "" {
		st.attachPathErr = tr("请输入至少一个文件路径", "Enter at least one file path")
		return false
	}
	valid, invalid := uikit.ResolveAttachmentPaths(raw)
	messages := homeManualAttachmentErrors(invalid, tr)
	if len(valid) > 0 {
		before := len(st.attachments)
		stageHomeAttachments(st, valid, tr)
		if len(st.attachments) == before && st.attachErr == "" {
			// Defensive: the resolver said the paths exist but staging read none
			// of them, so tell the user instead of closing silently.
			messages = append(messages, tr("未能添加所选文件", "None of the files could be added"))
		}
		// The staging error belongs to this dialog while it is open; showing it
		// again under the composer would duplicate it.
		if st.attachErr != "" {
			messages = append(messages, st.attachErr)
			st.attachErr = ""
		}
	}
	if len(messages) == 0 {
		st.attachPathErr = ""
		return true
	}
	st.attachPathErr = strings.Join(messages, "; ")
	return false
}

// buildHomeAttachmentPathDialog is Home's manual path-entry fallback (ZK-09): a
// native Modal for environments where the native file chooser is unavailable or
// blocks. It shares Home's staging path, so chips/MIME behave identically.
func buildHomeAttachmentPathDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *homeComposerState, tr func(zh, en string) string) {
	if !st.attachPathsOpen {
		return
	}
	add := func() {
		if applyHomeManualAttachmentPaths(st, st.attachPathDraft, tr) {
			st.attachPathsOpen = false
			st.attachPathDraft = ""
		}
		runtime.Invalidate()
	}
	closeDialog := func() {
		st.attachPathsOpen = false
		st.attachPathDraft = ""
		st.attachPathErr = ""
		runtime.Invalidate()
	}
	ui.Modal(c, &st.attachPathsOpen, func() {
		dialogEnter(c, add)
		design.Apply(ui.Column(c).Width(design.DialogWidthCompact).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, tr("手动添加附件", "Add attachments by path")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, tr(
				"输入文件的绝对路径，每行一个（Shift+Enter 换行）；也可以用逗号或分号分隔。",
				"Enter absolute file paths, one per line (Shift+Enter for a newline); commas or semicolons also work.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextArea(c, &st.attachPathDraft).Label(tr("文件路径", "File paths")).
				Placeholder("/home/you/notes.txt").FillWidth().MinHeight(design.Space6 * 3).
				OnTextKey(func(k ui.TextKey) bool {
					// Shift+Enter keeps the multiline list; a bare Enter adds.
					if k.Key != ui.KeyEnter || k.Mods != 0 {
						return false
					}
					add()
					return true
				})
			if st.attachPathErr != "" {
				uikit.ErrorLine(c, st.attachPathErr)
			}
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: tr("取消", "Cancel"), OnClick: closeDialog},
				{Label: tr("添加", "Add"), Primary: true,
					Disabled: strings.TrimSpace(st.attachPathDraft) == "",
					OnClick:  add},
			})
		})
	})
}

// dialogEnter runs a modal's primary action on Enter from the overlay scope, so
// the key reaches it wherever the focus is; a focused text area takes Enter
// through its own OnTextKey first (see the path dialog).
func dialogEnter(c *ui.Context, action func()) {
	if action == nil {
		return
	}
	c.OnShortcut(0, ui.KeyEnter, action)
}

// openHomeAttachmentPathDialog requests the manual path-entry fallback modal.
func openHomeAttachmentPathDialog(st *homeComposerState, runtime contracts.UIRuntime) {
	st.attachPathsOpen = true
	st.attachPathErr = ""
	runtime.Invalidate()
}

// addHomeWorkspace opens the native folder picker and registers the chosen
// folder as a workspace, the Home empty state's second next step.
func addHomeWorkspace(workspace contracts.WorkspaceService, st *homeComposerState, tr func(zh, en string) string) {
	dir, err := WorkspaceDirectoryPicker()
	switch {
	case err != nil:
		st.nextStepErr = tr("无法打开文件夹选择器：", "Could not open the folder picker: ") + err.Error()
		return
	case strings.TrimSpace(dir) == "":
		st.nextStepErr = ""
		return
	}
	name := filepath.Base(dir)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = dir
	}
	if _, err := workspace.Create(context.Background(), name, dir); err != nil {
		st.nextStepErr = tr("添加工作区失败：", "Could not add the workspace: ") + err.Error()
		return
	}
	st.nextStepErr = ""
}

// removeHomeAttachment drops one staged attachment.
func removeHomeAttachment(st *homeComposerState, id string) {
	kept := st.attachments[:0]
	for _, attachment := range st.attachments {
		if attachment.ID != id {
			kept = append(kept, attachment)
		}
	}
	st.attachments = kept
}

// formatHomeAttachmentSize renders a chip size in B/KB/MB.
func formatHomeAttachmentSize(size int) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}

// effectiveHomeModel reports the active session's model (or the configured
// default) for the read-only model chip.
func effectiveHomeModel(chat contracts.ChatService) string {
	if chat == nil {
		return ""
	}
	if active := chat.ActiveSession(); active != "" {
		if session, ok := chat.Session(active); ok && strings.TrimSpace(session.Model) != "" {
			return session.Model
		}
	}
	return chat.Config().Model
}

// openHomeSettingsPage deep-links the settings surface to one page when the
// host navigator supports it, and toggles the surface otherwise.
func openHomeSettingsPage(nav contracts.Navigator, pageID string) {
	if opener, ok := nav.(interface{ OpenSettingsPage(string) error }); ok {
		if err := opener.OpenSettingsPage(pageID); err == nil {
			return
		}
	}
	nav.ToggleSettings()
}

// buildHomeModelControl is the composer's model picker: the model catalog's
// presets write the default chat model for new conversations; without a catalog
// the control deep-links to the provider settings page.
// buildHomeAttachmentMenu is Home's single attach control: choose files through
// the native picker (off the UI thread) or enter absolute paths manually from
// the same menu.
func buildHomeAttachmentMenu(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *homeComposerState, tr func(zh, en string) string) {
	label := tr("附件", "Attach")
	tip := tr("添加文件或图片，或手动输入路径", "Add files or images, or enter a path")
	if st.attachBusy {
		label = tr("选择中…", "Choosing…")
		tip = tr("文件选择器打开中…", "The file picker is open…")
	}
	button := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if m.Item(tr("选择文件…", "Choose files…")).Disabled(st.attachBusy).Chosen() {
			startHomeAttachmentPick(runtime, st, tr)
		}
		if m.Item(tr("手动输入路径…", "Enter a path manually…")).
			Disabled(st.attachBusy).Chosen() {
			openHomeAttachmentPathDialog(st, runtime)
		}
	}).Tooltip(tip)
	design.Apply(button, t.TextMuted(design.TypeXS))
}

// buildHomeWorkspaceControl is Home's workspace chip: it names the workspace a
// new chat will belong to (the thing the sidebar groups by, but Home otherwise
// hid) and opens a menu that switches the workspace through the existing
// WorkspaceService, or adds one through the same native folder picker the
// empty state uses. One quiet menu button, not a second settings form.
func buildHomeWorkspaceControl(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	kctx cordis.Context, st *homeComposerState, tr func(zh, en string) string) {
	var workspace contracts.WorkspaceService
	if raw, ok := kctx.Get(contracts.KeyWorkspace); ok {
		workspace, _ = raw.(contracts.WorkspaceService)
	}
	if workspace == nil {
		return
	}
	current, hasCurrent, err := workspace.Current(context.Background())
	if err != nil {
		return
	}
	label := tr("选择工作区", "Choose workspace")
	tip := tr("新会话将属于这个工作区；点击切换", "New chats belong to this workspace; click to switch")
	if hasCurrent {
		label = homeChipLabel(homeWorkspaceName(current))
		if root := strings.TrimSpace(current.Root); root != "" {
			tip = root
		}
	}
	listed, _ := workspace.List(context.Background())
	button := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if len(listed) == 0 {
			m.Item(tr("还没有工作区", "No workspaces yet")).Disabled(true)
		}
		for _, ws := range listed {
			ws := ws
			item := m.Item(homeWorkspaceName(ws))
			item.Checked(hasCurrent && ws.ID == current.ID)
			if root := strings.TrimSpace(ws.Root); root != "" {
				item.Subtitle(root)
			}
			if !item.Chosen() || (hasCurrent && ws.ID == current.ID) {
				continue
			}
			if _, openErr := workspace.Open(context.Background(), ws.ID); openErr != nil {
				st.workspaceErr = tr("切换工作区失败：", "Could not switch the workspace: ") + openErr.Error()
			} else {
				st.workspaceErr = ""
			}
			runtime.Invalidate()
		}
		m.Separator()
		if m.Item(tr("添加工作区…", "Add workspace…")).Chosen() {
			dir, pickErr := WorkspaceDirectoryPicker()
			switch {
			case pickErr != nil:
				st.workspaceErr = tr("无法打开目录选择器：", "Could not open the folder picker: ") + pickErr.Error()
			case strings.TrimSpace(dir) == "":
				// Canceled: nothing changes.
			default:
				if _, createErr := workspace.Create(context.Background(), "", dir); createErr != nil {
					st.workspaceErr = tr("添加工作区失败：", "Could not add the workspace: ") + createErr.Error()
				} else {
					st.workspaceErr = ""
				}
			}
			runtime.Invalidate()
		}
	}).Tooltip(tip)
	design.Apply(button, t.TextMuted(design.TypeXXXS))
}

// homeWorkspaceName is a workspace's display name: its name, or the folder path
// when the backend derived no name.
func homeWorkspaceName(workspace contracts.Workspace) string {
	if name := strings.TrimSpace(workspace.Name); name != "" {
		return name
	}
	return strings.TrimSpace(workspace.Root)
}

// homeChipLabel bounds one context chip's text so a long workspace name cannot
// push the send action out of the row; the full name stays in the tooltip.
func homeChipLabel(label string) string {
	const maxRunes = 18
	runes := []rune(strings.TrimSpace(label))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes-1]) + "…"
}

func buildHomeModelControl(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	nav contracts.Navigator, chat contracts.ChatService, kctx cordis.Context,
	settings contracts.SettingsService, st *homeComposerState, english bool,
	tr func(zh, en string) string) {
	label := strings.TrimSpace(effectiveHomeModel(chat))
	if label == "" {
		label = tr("选择模型", "Choose model")
	}
	catalog, hasCatalog := kctx.Get(contracts.KeyModelCatalog)
	models := []contracts.ModelInfo(nil)
	if hasCatalog {
		if service, ok := catalog.(contracts.ModelCatalogService); ok && service != nil {
			models = service.Models()
		}
	}
	selector := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if len(models) == 0 {
			// The model list is configured on the model settings page (the old
			// "providers" page no longer exists).
			if m.Item(tr("打开模型设置…", "Open model settings…")).Chosen() {
				openHomeSettingsPage(nav, "model")
			}
			return
		}
		m.MaxWidth(design.OverlayWidthS)
		current := strings.TrimSpace(effectiveHomeModel(chat))
		// Two compact entries open the real choices one level down: the model
		// list (grouped by provider) and this model's thinking default, instead
		// of stacking every row in one menu.
		modelRow := m.Submenu(tr("模型", "Model"), func(sub *ui.Menu) {
			buildHomeModelChoices(sub, runtime, settings, current, models, english, tr)
		})
		modelRow.Trailing(homeDefaultModelLabel(models, current, tr))
		if info, found := contracts.FindModelInfo(models, current); found {
			buildHomeThinkingRow(m, runtime, st, settings, info, english, tr)
		}
	}).Tooltip(tr("选择新会话使用的模型与推理等级", "Choose the model and reasoning level for new conversations"))
	design.Apply(selector, t.TextMuted(design.TypeXXXS))
}

// homeProviderGroup names the provider group of one model row in Home's 模型
// submenu: the preset matching the row's own BaseURL, or the custom-service
// label. Rows keep the catalog order; a header is added whenever it changes.
func homeProviderGroup(english bool, info contracts.ModelInfo, tr func(zh, en string) string) string {
	if preset, ok := contracts.MatchProviderPreset(info.BaseURL); ok {
		return preset.DisplayName(english)
	}
	return tr("自定义服务", "Custom service")
}

// homeModelLabel names one catalog row: its display name, or its id.
func homeModelLabel(info contracts.ModelInfo) string {
	return firstNonEmpty(strings.TrimSpace(info.Name), strings.TrimSpace(info.ID))
}

// homeDefaultModelLabel names the default model for the compact menu: the
// catalog display name when it is configured, else its id, else the
// not-configured copy.
func homeDefaultModelLabel(models []contracts.ModelInfo, modelID string,
	tr func(zh, en string) string) string {
	if info, ok := contracts.FindModelInfo(models, modelID); ok {
		return homeModelLabel(info)
	}
	if model := strings.TrimSpace(modelID); model != "" {
		return model
	}
	return tr("未配置", "Not set")
}

// buildHomeModelChoices fills Home's 模型 submenu: the default-model candidates
// grouped by provider (a disabled header per provider group, in catalog order),
// each row radio-marking the current default model and writing chat.model when
// chosen.
func buildHomeModelChoices(m *ui.Menu, runtime contracts.UIRuntime,
	settings contracts.SettingsService, current string, models []contracts.ModelInfo,
	english bool, tr func(zh, en string) string) {
	group := ""
	for _, model := range models {
		model := model
		if provider := homeProviderGroup(english, model, tr); provider != group {
			m.Item(provider).Disabled(true)
			group = provider
		}
		item := m.Item(homeModelLabel(model))
		if note := strings.TrimSpace(model.Note); note != "" {
			item.Subtitle(note)
		}
		if model.ContextWindow > 0 {
			item.Trailing(homeFormatContextWindow(model.ContextWindow))
		}
		item.Radio(strings.EqualFold(strings.TrimSpace(model.ID), strings.TrimSpace(current)))
		if item.Chosen() && settings != nil {
			_ = settings.Set(contracts.SettingsChatModel, model.ID)
			runtime.Invalidate()
		}
	}
}

// homeFormatContextWindow renders a token count for the model submenu's trailing
// value: exact thousands as "128k", other positive values with one decimal
// ("32.8k"); 0 (unknown) renders nothing.
func homeFormatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens%1000 == 0 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%.1fk", float64(tokens)/1000)
}

// homeModelThinkingLabel names one model's configured default thinking mode: the
// mode's display label, "系列默认" when the row follows the family default
// (empty DefaultThinking), or the raw ID for a mode the row no longer defines.
func homeModelThinkingLabel(info contracts.ModelInfo, english bool, tr func(zh, en string) string) string {
	id := strings.TrimSpace(info.DefaultThinking)
	if id == "" {
		return tr("系列默认", "Family default")
	}
	if mode, ok := contracts.ThinkingModeByID(contracts.ThinkingModesForModel(info), id); ok {
		return mode.DisplayName(english)
	}
	return id
}

// persistHomeModelDefaultThinking rewrites one model row's DefaultThinking in the
// modelcatalog.models settings row. The whole list is decoded and re-encoded so
// the row order and every other field of every model survive; a model ID the
// stored list does not contain is an error (there is no row to edit).
func persistHomeModelDefaultThinking(settings contracts.SettingsService, modelID, modeID string) error {
	if settings == nil {
		return fmt.Errorf("no settings store")
	}
	models, err := contracts.LoadModelCatalog(settings)
	if err != nil {
		return err
	}
	want := strings.TrimSpace(modelID)
	for index := range models {
		if !strings.EqualFold(strings.TrimSpace(models[index].ID), want) {
			continue
		}
		models[index].DefaultThinking = strings.TrimSpace(modeID)
		encoded, encodeErr := contracts.EncodeModelInfoList(models)
		if encodeErr != nil {
			return encodeErr
		}
		return settings.Set(contracts.SettingModelCatalogModels, encoded)
	}
	return fmt.Errorf("model %q is not in the model list", want)
}

// buildHomeThinkingRow adds Home's compact 推理等级 row: its submenu edits the
// default model's DefaultThinking (the model-level strength new conversations
// follow), writing the modelcatalog.models row. Reports whether the row was
// added: a model without selectable modes or a missing settings store hides it.
func buildHomeThinkingRow(m *ui.Menu, runtime contracts.UIRuntime, st *homeComposerState,
	settings contracts.SettingsService, info contracts.ModelInfo, english bool,
	tr func(zh, en string) string) bool {
	modelID := strings.TrimSpace(info.ID)
	modes := contracts.ThinkingModesForModel(info)
	if settings == nil || modelID == "" || len(modes) == 0 {
		return false
	}
	configured := strings.TrimSpace(info.DefaultThinking)
	save := func(modeID string) {
		if err := persistHomeModelDefaultThinking(settings, info.ID, modeID); err != nil {
			st.modelErr = tr("保存模型思考强度失败：", "Could not save the model thinking default: ") + err.Error()
		} else {
			st.modelErr = ""
		}
		runtime.Invalidate()
	}
	row := m.Submenu(tr("推理等级", "Reasoning level"), func(sub *ui.Menu) {
		family := sub.Item(tr("系列默认", "Family default"))
		family.Radio(configured == "")
		if family.Chosen() {
			save("")
		}
		for _, mode := range modes {
			mode := mode
			item := sub.Item(mode.DisplayName(english))
			item.Radio(strings.EqualFold(configured, mode.ID))
			if item.Chosen() {
				save(mode.ID)
			}
		}
	})
	row.Trailing(homeModelThinkingLabel(info, english, tr))
	return true
}

// homeModeChoice is one Home context-mode entry; it writes the same agent/chat
// settings pair as the session composer's mode menu.
type homeModeChoice struct {
	agent string
	chat  string
	zh    string
	en    string
}

var homeModeChoices = []homeModeChoice{
	{agent: contracts.AgentModeNormal, chat: contracts.ChatModeStandard, zh: "普通", en: "Normal"},
	{agent: contracts.AgentModePlan, chat: contracts.ChatModeStandard, zh: "计划", en: "Plan"},
	{agent: contracts.AgentModeNormal, chat: contracts.ChatModeMinimal, zh: "精简", en: "Minimal"},
	{agent: contracts.AgentModeNormal, chat: contracts.ChatModeCreator, zh: "创作", en: "Creator"},
}

// buildHomeModeControl is the composer's chat-mode picker; Home can switch the
// mode for the conversation it is about to start.
func buildHomeModeControl(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	settings contracts.SettingsService, tr func(zh, en string) string) {
	if settings == nil {
		return
	}
	label := homeModeChoiceLabel(settings, tr)
	ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		for _, choice := range homeModeChoices {
			choice := choice
			item := m.Item(tr(choice.zh, choice.en))
			item.Checked(choice.agent == strings.TrimSpace(settings.Get(contracts.SettingAgentMode, "")) &&
				choice.chat == effectiveChatMode(settings))
			if item.Chosen() {
				_ = settings.Set(contracts.SettingAgentMode, choice.agent)
				_ = settings.Set(contracts.SettingChatMode, choice.chat)
				runtime.Invalidate()
			}
		}
	}).Tooltip(tr("选择新会话的对话模式", "Choose the mode for new conversations"))
}

// effectiveChatMode normalizes the stored chat mode (standard when unset).
func effectiveChatMode(settings contracts.SettingsService) string {
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingChatMode, ""))) {
	case contracts.ChatModeMinimal:
		return contracts.ChatModeMinimal
	case contracts.ChatModeCreator:
		return contracts.ChatModeCreator
	default:
		return contracts.ChatModeStandard
	}
}

// homeModeChoiceLabel is the current combined agent/chat mode label.
func homeModeChoiceLabel(settings contracts.SettingsService, tr func(zh, en string) string) string {
	if strings.EqualFold(strings.TrimSpace(settings.Get(contracts.SettingAgentMode, "")), contracts.AgentModePlan) {
		return tr("计划", "Plan")
	}
	switch effectiveChatMode(settings) {
	case contracts.ChatModeMinimal:
		return tr("精简", "Minimal")
	case contracts.ChatModeCreator:
		return tr("创作", "Creator")
	default:
		return tr("普通", "Normal")
	}
}

// submitHomeComposer creates a session, sends the draft (with attachments when
// the backend supports them) and navigates to the conversation screen.
func submitHomeComposer(chat contracts.ChatService, nav contracts.Navigator, runtime contracts.UIRuntime,
	st *homeComposerState, tr func(zh, en string) string) bool {
	if chat == nil || st.sending || strings.TrimSpace(st.draft) == "" {
		return false
	}
	prompt := strings.TrimSpace(st.draft)
	attachments := append([]contracts.Attachment(nil), st.attachments...)
	st.sending = true
	st.sendErr = ""
	id, err := chat.NewSession("")
	if err != nil {
		st.sendErr = tr("发送失败：", "Send failed: ") + err.Error()
		st.sending = false
		runtime.Invalidate()
		return false
	}
	if len(attachments) > 0 {
		if sender, ok := chat.(contracts.AttachmentSender); ok {
			err = sender.SendWithAttachments(id, prompt, attachments)
		} else {
			err = chat.Send(id, prompt)
			st.notice = tr("附件需在会话中发送", "Attachments must be sent in a conversation")
		}
	} else {
		err = chat.Send(id, prompt)
	}
	if err != nil {
		st.sendErr = tr("发送失败：", "Send failed: ") + err.Error()
		st.sending = false
		runtime.Invalidate()
		return false
	}
	st.draft = ""
	st.attachments = nil
	st.attachErr = ""
	st.sendErr = ""
	st.sending = false
	_ = nav.Push("session")
	runtime.Invalidate()
	return true
}

// buildHomeComposer renders the Home composer card through the shared
// ComposerShell: the same card, input, width policy, send position and key
// semantics as the conversation composer. Home keeps its own business slots —
// the staged attachment chips, the workspace/model/conversation-mode toolbar and
// its error/notice rows — and sends through submitHomeComposer.
func buildHomeComposer(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, kctx cordis.Context,
	st *homeComposerState, placeholder string, tr func(zh, en string) string) {
	var settings contracts.SettingsService
	if raw, ok := kctx.Get(contracts.KeySettings); ok {
		settings, _ = raw.(contracts.SettingsService)
	}
	var locale contracts.LocaleService
	if raw, ok := kctx.Get(contracts.KeyLocale); ok {
		locale, _ = raw.(contracts.LocaleService)
	}
	placeholderText := strings.TrimSpace(placeholder)
	sendLabel := tr("发送", "Send")
	if st.sending {
		sendLabel = tr("发送中…", "Sending…")
	}
	errors := make([]string, 0, 4)
	for _, text := range []string{st.attachErr, st.workspaceErr, st.modelErr, st.sendErr} {
		if strings.TrimSpace(text) != "" {
			errors = append(errors, text)
		}
	}
	composerServices := c.Services()
	uikit.ComposerShell(c, uikit.ComposerShellOptions{
		Key:         "home.composer",
		Draft:       &st.draft,
		Placeholder: placeholderText,
		CardLabel:   tr("输入框", "Composer"),
		FullWidth:   true,
		Locale:      locale,
		OnPasteImage: func() bool {
			stageHomePastedImage(st, composerServices, tr)
			runtime.Invalidate()
			return true
		},
		Attachments: func(ic *ui.Context) {
			if len(st.attachments) == 0 {
				return
			}
			ui.Row(ic).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				for _, attachment := range st.attachments {
					attachment := attachment
					design.Apply(ui.Row(ic).AlignItems(ui.Center).Gap(design.Space1).
						Padding(design.Space1, design.Space2).Radius(design.RadiusSM).
						Border(1, t.BorderL2), func(el ui.Element) ui.Element { return el }).Children(func() {
						design.Apply(ui.Text(ic, attachment.Name+" · "+formatHomeAttachmentSize(len(attachment.Data))),
							t.Font(design.TypeXXS)).TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
						if ui.Button(ic, tr("移除", "Remove")).Clicked() {
							removeHomeAttachment(st, attachment.ID)
							runtime.Invalidate()
						}
					})
				}
			})
		},
		Notices: func(ic *ui.Context) {
			// A missing API key is a pre-send warning, not a hard block: local
			// model servers need no key, so the send path stays available.
			if _, missingKey := homeConfigGaps(chat); missingKey {
				design.Apply(ui.Text(ic, tr(
					"尚未配置 API Key：发送可能失败；本地模型服务可忽略。",
					"No API key configured: sending may fail; ignore this for local model servers.")),
					t.Font(design.TypeXXS)).TextColor(t.WarningText).MaxLines(2)
			}
		},
		Toolbar: func(ic *ui.Context) {
			buildHomeAttachmentMenu(ic, t, runtime, st, tr)
			if st.attachBusy {
				// The picker runs off the UI thread: the official spinner shows
				// the wait next to the menu whose label also says 选择中….
				ui.Spinner(ic).Label(tr("选择中…", "Choosing…")).
					Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
			}
			buildHomeWorkspaceControl(ic, t, runtime, kctx, st, tr)
			buildHomeModelControl(ic, t, runtime, navigator, chat, kctx, settings, st,
				uikit.Language(locale) == "en", tr)
			buildHomeModeControl(ic, t, runtime, settings, tr)
		},
		SendEnabled: chat != nil && !st.sending && strings.TrimSpace(st.draft) != "",
		SendLabel:   sendLabel,
		SendTooltip: tr("发送 (Enter)", "Send (Enter)"),
		Send: func() bool {
			return submitHomeComposer(chat, navigator, runtime, st, tr)
		},
		Errors: errors,
		Notice: st.notice,
	})
	// The manual attachment-path fallback is a Modal above the composer, as on
	// the conversation screen.
	buildHomeAttachmentPathDialog(c, t, runtime, st, tr)
}
