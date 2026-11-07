package session

// Command-locale registry: the host command palette reads command titles/hints
// from the runtime registry, so the session slash commands are re-registered
// when the host language changes (otherwise an English product or a language
// switch would leave the command menu in Chinese).

import (
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// commandRegistry owns the session command contributions and keeps them in the
// host locale. It is only touched on the UI thread.
type commandRegistry struct {
	runtime    contracts.UIRuntime
	navigator  contracts.Navigator
	chat       contracts.ChatService
	config     Config
	st         *state
	lang       string
	registered []func()
}

// newCommandRegistry builds the registry for one module instance.
func newCommandRegistry(runtime contracts.UIRuntime, navigator contracts.Navigator,
	chat contracts.ChatService, config Config, st *state) *commandRegistry {
	return &commandRegistry{runtime: runtime, navigator: navigator, chat: chat, config: config, st: st}
}

// start registers the commands for the activation locale.
func (r *commandRegistry) start() error { return r.registerLocalized() }

// registerLocalized registers every session command with the current locale's
// titles. On a mid-list failure the partial registrations are reverted.
func (r *commandRegistry) registerLocalized() error {
	commands := sessionCommands(r.runtime, r.navigator, r.chat, r.config, r.st)
	registered := make([]func(), 0, len(commands))
	for _, command := range commands {
		unregister, err := r.runtime.RegisterCommand(command)
		if err != nil {
			for index := len(registered) - 1; index >= 0; index-- {
				registered[index]()
			}
			return err
		}
		registered = append(registered, unregister)
	}
	r.registered = registered
	r.lang = r.localeLanguage()
	return nil
}

// localeLanguage is the host language the registered titles were built with.
func (r *commandRegistry) localeLanguage() string {
	if r.st == nil || r.st.locale == nil {
		return ""
	}
	return uikit.Language(r.st.locale)
}

// sync re-registers the commands when the host language changed since the last
// frame. It is called from the screen build so the slash menu and the command
// palette follow a language switch on the next frame.
func (r *commandRegistry) sync() {
	if r.localeLanguage() == r.lang {
		return
	}
	r.close()
	_ = r.registerLocalized()
}

// close unregisters every current command contribution.
func (r *commandRegistry) close() {
	for _, unregister := range r.registered {
		unregister()
	}
	r.registered = nil
}
