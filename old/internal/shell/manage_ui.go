package shell

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/icons"
)

// PD-07: management pages for the two file/setting based extension points and
// the workspace instruction file. Everything here is frontend-only: MCP server
// configuration lives in the mcp.servers setting, skills are folders with a
// SKILL.md file under skills.root, and AGENTS.md is a workspace file. The app
// reads the same sources when it builds the next agent, so the pages only
// validate and write them.

const (
	settingsTabMCPServers settingsTab = settingsTabDesktop + 1
	settingsTabSkills     settingsTab = settingsTabDesktop + 2

	settingMCPServers       = "mcp.servers"
	settingMCPWorkspaceMode = "mcp.workspace_config"
	settingSkillsRoot       = "skills.root"
)

// mcpServerPreview is one parsed mcp.servers entry: the fields the page shows
// in the summary list. Unknown fields stay in the JSON the user edits.
type mcpServerPreview struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	URL       string   `json:"url"`
}

// validateMCPServers parses the JSON editor's text. It returns the entries for
// the summary list or the parse/validation error the page shows inline; the
// page never saves invalid JSON.
func validateMCPServers(raw string) ([]mcpServerPreview, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var servers []mcpServerPreview
	if err := json.Unmarshal([]byte(trimmed), &servers); err != nil {
		return nil, fmt.Errorf("JSON: %w", err)
	}
	if servers == nil {
		return nil, fmt.Errorf("expected a JSON array of servers")
	}
	seen := make(map[string]bool, len(servers))
	for _, server := range servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			return nil, fmt.Errorf("a server is missing its name")
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate server name %q", name)
		}
		seen[name] = true
		if strings.TrimSpace(server.Command) == "" && strings.TrimSpace(server.URL) == "" {
			return nil, fmt.Errorf("server %q needs a command or a url", name)
		}
	}
	return servers, nil
}

// mcpSettingsPage is Settings > MCP servers: the configured servers, their
// source (settings wins over the workspace file), and a JSON editor that
// validates before writing mcp.servers.
func (a *App) mcpSettingsPage(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	if !st.mcpLoaded {
		st.mcpDraft = a.shellSetting(settingMCPServers, "")
		st.mcpLoaded = true
	}
	servers, parseErr := validateMCPServers(st.mcpDraft)
	a.settingsPageHeader(c, t,
		a.menuText("MCP servers", "MCP 服务"),
		a.menuText("Model Context Protocol servers the agent can call. The settings value wins over a workspace mcp.json.",
			"代理可调用的模型上下文协议服务。设置中的值优先于工作区的 mcp.json。"))
	a.settingsSection(c, t, a.menuText("Configured servers", "已配置服务"), func() {
		switch {
		case strings.TrimSpace(st.mcpDraft) == "":
			a.shellInfoRow(c, t, a.menuText("Servers", "服务"),
				a.menuText("None configured", "尚未配置"), true)
		case parseErr != nil:
			a.shellInfoRow(c, t, a.menuText("Servers", "服务"),
				a.menuText("Invalid JSON, fix it below", "JSON 无效，请在下方修正"), true)
		default:
			for i, server := range servers {
				detail := strings.TrimSpace(server.Command)
				if detail == "" {
					detail = strings.TrimSpace(server.URL)
				}
				if len(server.Args) > 0 {
					detail += " " + strings.Join(server.Args, " ")
				}
				a.shellInfoRow(c, t, server.Name, detail, i == len(servers)-1)
			}
		}
	})
	a.settingsSection(c, t, a.menuText("Configuration", "配置"), func() {
		ui.Column(c).Padding(design.Space3, design.Space4).Gap(design.Space2).Children(func() {
			a.settingsTypeHint(c, t, a.menuText(
				"JSON array of {name, command, args, env} for stdio servers or {name, transport: \"http\", url} for remote ones. Changes apply to the next turn.",
				"stdio 服务用 {name, command, args, env} 数组；远程服务用 {name, transport: \"http\", url}。修改在下一回合生效。"))
			editor := ui.TextArea(c, &st.mcpDraft).
				Label(a.menuText("MCP servers JSON", "MCP 服务 JSON")).
				Placeholder(`[{"name": "files", "command": "npx", "args": ["-y", "server-files"]}]`).
				MinHeight(t.Theme().Space(30)).Font(monoFont).FontSize(12)
			_ = editor
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				label := a.menuText("Save servers", "保存服务")
				if ui.Button(c, label).Height(t.Theme().Space(8)).Label(label).Clicked() {
					a.saveMCPServers(c)
				}
				if parseErr != nil {
					ui.Text(c, parseErr.Error()).FontSize(11).TextColor(t.Danger).MaxLines(2)
				} else if strings.TrimSpace(st.mcpDraft) != "" {
					ui.Text(c, a.menuText("JSON looks valid", "JSON 校验通过")).FontSize(11).
						TextColor(t.LabelTertiary)
				}
			})
		})
	})
	a.settingsSection(c, t, a.menuText("Workspace file", "工作区文件"), func() {
		mode := a.shellSetting(settingMCPWorkspaceMode, "off")
		desc := a.menuText("Workspace mcp.json is ignored (mcp.workspace_config="+mode+"). A cloned repository cannot start commands by default.",
			"工作区 mcp.json 被忽略（mcp.workspace_config="+mode+"）。克隆的仓库默认无法启动命令。")
		a.shellInfoRow(c, t, a.menuText("Workspace config", "工作区配置"), desc, true)
	})
}

// saveMCPServers validates and persists the editor text.
func (a *App) saveMCPServers(c *ui.Context) {
	st := a.shellUI()
	if _, err := validateMCPServers(st.mcpDraft); err != nil {
		a.toastDeferred(c, a.menuText("MCP servers not saved: ", "MCP 服务未保存：")+err.Error())
		return
	}
	if err := a.setShellSetting(settingMCPServers, strings.TrimSpace(st.mcpDraft)); err != nil {
		a.toastDeferred(c, a.menuText("Could not save the MCP servers: ", "无法保存 MCP 服务：")+err.Error())
		return
	}
	a.toastDeferred(c, a.menuText("MCP servers saved", "MCP 服务已保存"))
}

// settingsTypeHint draws a small explanatory line inside a settings card.
func (a *App) settingsTypeHint(c *ui.Context, t *design.Tokens, text string) {
	settingsTypeText(c, t, design.TypeXXS, t.LabelTertiary, text).MaxLines(3)
}

// skillPreview is one discovered SKILL.md: its folder, the parsed name and
// description, and a validation problem when the front matter is incomplete.
type skillPreview struct {
	Dir         string
	Path        string
	Name        string
	Description string
	Err         string
}

// skillsRoots returns the skill roots the app scans: the configured
// skills.root and the workspace's own skills folder, de-duplicated.
func (a *App) skillsRoots() []string {
	roots := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		roots = append(roots, abs)
	}
	add(a.shellSetting(settingSkillsRoot, "skills"))
	if root := a.workspaceRoot(); root != "" {
		add(filepath.Join(root, "skills"))
	}
	return roots
}

// scanSkills lists the SKILL.md folders under the roots.
func (a *App) scanSkills() []skillPreview {
	out := make([]skillPreview, 0, 8)
	for _, root := range a.skillsRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			path := filepath.Join(dir, "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			skill := skillPreview{Dir: dir, Path: path, Name: entry.Name()}
			skill.Name, skill.Description = parseSkillFrontMatter(string(data))
			if strings.TrimSpace(skill.Name) == "" {
				skill.Name = entry.Name()
				skill.Err = a.menuText("missing a name in the front matter", "front matter 缺少 name")
			}
			if strings.TrimSpace(skill.Description) == "" {
				if skill.Err == "" {
					skill.Err = a.menuText("missing a description in the front matter", "front matter 缺少 description")
				}
			}
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseSkillFrontMatter reads the "name:" and "description:" lines of the
// leading --- front-matter block. A missing block leaves both empty so the
// page can flag it.
func parseSkillFrontMatter(text string) (name, description string) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

// skillsSettingsPage is Settings > Skills: the roots, the discovered skills
// with their validation state, and the workspace AGENTS.md editor.
func (a *App) skillsSettingsPage(c *ui.Context, t *design.Tokens) {
	skills := a.scanSkills()
	a.settingsPageHeader(c, t,
		a.menuText("Skills", "技能"),
		a.menuText("Folders with a SKILL.md file the agent can load, plus the workspace AGENTS.md instructions.",
			"带有 SKILL.md 的文件夹构成代理可加载的技能；此外可编辑工作区 AGENTS.md 指令。"))
	a.settingsSection(c, t, a.menuText("Roots", "根目录"), func() {
		a.shellTextRow(c, t,
			a.menuText("Skills root", "技能根目录"),
			a.menuText("The configured root is scanned along with <workspace>/skills. Relative paths resolve against the app's working directory.",
				"配置的根目录会与 <工作区>/skills 一起扫描；相对路径相对于应用工作目录。"),
			settingSkillsRoot, "skills", "skills", false)
		for i, root := range a.skillsRoots() {
			a.shellInfoRow(c, t, a.menuText("Scan path", "扫描路径"), root, i == len(a.skillsRoots())-1)
		}
	})
	a.settingsSection(c, t, a.menuText("Discovered skills", "已发现技能"), func() {
		if len(skills) == 0 {
			a.shellInfoRow(c, t, a.menuText("Skills", "技能"),
				a.menuText("No SKILL.md found under the roots", "根目录下未找到 SKILL.md"), true)
			return
		}
		for i, skill := range skills {
			desc := skill.Description
			if skill.Err != "" {
				desc = skill.Err
			}
			last := i == len(skills)-1
			a.settingsRow(c, t, skill.Name, desc+" · "+skill.Path, settingsRowStyle{last: last}, func() {
				label := a.menuText("Open folder", "打开文件夹")
				if ui.Button(c, label).Height(t.Theme().Space(8)).Label(label).Clicked() {
					if err := a.pluginOpener()(skill.Dir); err != nil {
						a.toastDeferred(c, a.menuText("Could not open the folder", "无法打开文件夹"))
					}
				}
			})
		}
	})
	a.agentsMDSection(c, t)
	a.agentsMDDialog(c, t)
}

// agentsMDPath is the instruction file of the current workspace.
func (a *App) agentsMDPath() string {
	root := a.workspaceRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "AGENTS.md")
}

// agentsMDSection shows the workspace AGENTS.md state and its two actions:
// edit the existing file, or create it from a template.
func (a *App) agentsMDSection(c *ui.Context, t *design.Tokens) {
	path := a.agentsMDPath()
	if path == "" {
		a.settingsSection(c, t, a.menuText("AGENTS.md", "AGENTS.md"), func() {
			a.shellInfoRow(c, t, a.menuText("File", "文件"),
				a.menuText("Select a workspace first", "请先选择工作区"), true)
		})
		return
	}
	data, err := os.ReadFile(path)
	exists := err == nil
	a.settingsSection(c, t, a.menuText("AGENTS.md", "AGENTS.md"), func() {
		state := a.menuText("Not created yet", "尚未创建")
		if exists {
			state = a.menuText("Present · ", "已存在 · ") + path
		}
		a.settingsRow(c, t, a.menuText("Workspace instructions", "工作区指令"), state,
			settingsRowStyle{last: true}, func() {
				label := a.menuText("Edit", "编辑")
				if !exists {
					label = a.menuText("Create", "创建")
				}
				if ui.Button(c, label).Height(t.Theme().Space(8)).Label(label).Clicked() {
					a.beginAgentsMDEdit(string(data), exists)
				}
			})
	})
}

// beginAgentsMDEdit opens the editor, seeded with the file or the template.
func (a *App) beginAgentsMDEdit(current string, exists bool) {
	st := a.shellUI()
	if !exists {
		current = "# AGENTS.md\n\nInstructions for the agent in this workspace.\n\n## Project notes\n\n- \n"
	}
	st.agentsDraft = current
	st.agentsEditing = true
	st.agentsErr = ""
}

// agentsMDDialog is the AGENTS.md editor modal.
func (a *App) agentsMDDialog(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	if !st.agentsEditing {
		return
	}
	th := t.Theme()
	path := a.agentsMDPath()
	ui.Modal(c, &st.agentsEditing, func() {
		ui.Column(c).Width(th.Space(160)).Gap(th.Space(3)).Children(func() {
			ui.Column(c).Gap(th.Space(1)).Children(func() {
				settingsTypeText(c, t, design.TypeM, t.LabelPrimary,
					a.menuText("Edit AGENTS.md", "编辑 AGENTS.md")).FontWeight(600)
				settingsTypeText(c, t, design.TypeXXS, t.LabelTertiary, path).MaxLines(1)
			})
			ui.TextArea(c, &st.agentsDraft).Label(a.menuText("AGENTS.md content", "AGENTS.md 内容")).
				MinHeight(th.Space(60)).Font(monoFont).FontSize(12)
			if st.agentsErr != "" {
				ui.Text(c, st.agentsErr).FontSize(11).TextColor(t.Danger).MaxLines(2)
			}
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.menuText("Cancel", "取消")).Clicked() {
					st.agentsEditing = false
					st.agentsErr = ""
				}
				label := a.menuText("Save AGENTS.md", "保存 AGENTS.md")
				if ui.PrimaryButton(c, label).Clicked() {
					st.agentsErr = a.saveAgentsMD(st.agentsDraft)
					if st.agentsErr == "" {
						st.agentsEditing = false
						a.toastDeferred(c, a.menuText("AGENTS.md saved", "AGENTS.md 已保存"))
					}
				}
			})
		})
	})
}

// saveAgentsMD writes the editor content to the workspace file, returning a
// user-facing error message ("" on success).
func (a *App) saveAgentsMD(text string) string {
	path := a.agentsMDPath()
	if path == "" {
		return a.menuText("Select a workspace first.", "请先选择工作区。")
	}
	if strings.TrimSpace(text) == "" {
		return a.menuText("AGENTS.md cannot be empty.", "AGENTS.md 不能为空。")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return a.menuText("Could not write AGENTS.md: ", "无法写入 AGENTS.md：") + err.Error()
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return a.menuText("Could not write AGENTS.md: ", "无法写入 AGENTS.md：") + err.Error()
	}
	return ""
}

// settingsPageIconForManagement is the nav glyph of the management pages.
func (a *App) settingsPageIconForManagement(tab settingsTab) *ui.SVG {
	switch tab {
	case settingsTabMCPServers:
		return icons.Plug()
	case settingsTabSkills:
		return icons.Sparkles()
	}
	return icons.Sliders()
}

// managementPages is the nav list of the management pages, in display order.
func (a *App) managementPages() []struct {
	label string
	tab   settingsTab
} {
	return []struct {
		label string
		tab   settingsTab
	}{
		{a.menuText("MCP servers", "MCP 服务"), settingsTabMCPServers},
		{a.menuText("Skills", "技能"), settingsTabSkills},
	}
}
