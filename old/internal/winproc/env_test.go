package winproc

import (
	"strings"
	"testing"
)

// TestScrubEnvRemovesCredentialVariables pins RT-15: the child environment must
// not carry provider keys, cloud/CI tokens or ZeoKeel state, while ordinary
// shell/build variables stay intact.
func TestScrubEnvRemovesCredentialVariables(t *testing.T) {
	env := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/user",
		"LANG=en_US.UTF-8",
		"TERM=xterm-256color",
		"GIT_AUTHOR_NAME=A User",
		"ZEOKEEL_DATA_DIR=/home/user/.zeokeel",
		"ZEOKEEL_MCP_SERVERS=[]",
		"DEEPSEEK_API_KEY=sk-deep",
		"DEEPSEEK_MODEL=deepseek-chat",
		"OPENAI_API_KEY=sk-openai",
		"ANTHROPIC_API_KEY=sk-ant",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"AWS_ACCESS_KEY_ID=aws-id",
		"AWS_SESSION_TOKEN=aws-token",
		"AZURE_CLIENT_SECRET=azure-secret",
		"GOOGLE_APPLICATION_CREDENTIALS=/tmp/creds.json",
		"GITHUB_TOKEN=ghp-secret",
		"SLACK_BOT_TOKEN=xoxb-secret",
		"HF_TOKEN=hf-secret",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		"KUBECONFIG=/home/user/.kube/config",
		"PGPASSWORD=pg-secret",
		"MYSQL_PWD=mysql-secret",
		"DATABASE_URL=postgres://user:pass@db/x",
		"APP_SECRET_KEY=base64secret",
		"MY_PRIVATE_KEY=-----BEGIN",
	}
	got := ScrubEnv(env)
	kept := make(map[string]bool, len(got))
	for _, entry := range got {
		kept[strings.SplitN(entry, "=", 2)[0]] = true
	}
	for _, want := range []string{"PATH", "HOME", "LANG", "TERM", "GIT_AUTHOR_NAME"} {
		if !kept[want] {
			t.Errorf("ScrubEnv removed ordinary variable %q", want)
		}
	}
	for _, entry := range env {
		name := strings.SplitN(entry, "=", 2)[0]
		switch name {
		case "PATH", "HOME", "LANG", "TERM", "GIT_AUTHOR_NAME":
			continue
		}
		if kept[name] {
			t.Errorf("ScrubEnv kept credential-bearing variable %q", name)
		}
	}
}

// TestIsSecretEnvName pins the matcher itself, including case-insensitivity and
// the names that only match the exact-name list.
func TestIsSecretEnvName(t *testing.T) {
	secret := []string{
		"ZEOKEEL_SESSIONS", "deepseek_api_key", "OPENAI_API_KEY", "aws_secret_access_key",
		"GITHUB_TOKEN", "MY_PASSWORD", "SOME_PASSWD", "DB_CREDENTIALS", "SSH_AUTH_SOCK",
		"KUBECONFIG", "MYSQL_PWD", "DATABASE_URL",
	}
	for _, name := range secret {
		if !IsSecretEnvName(name) {
			t.Errorf("IsSecretEnvName(%q) = false, want true", name)
		}
	}
	ordinary := []string{"", "PATH", "HOME", "LANG", "TERM", "PWD", "GIT_AUTHOR_NAME", "NODE_OPTIONS", "TZ"}
	for _, name := range ordinary {
		if IsSecretEnvName(name) {
			t.Errorf("IsSecretEnvName(%q) = true, want false", name)
		}
	}
}
