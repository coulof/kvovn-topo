package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleConfig = `
ui:
  theme: suse
  flash: 30s
  pane: false
contexts:
  harvester:
    daemon: {namespace: ovn-topology, service: topo, port: "8080"}
    k8sNamespace: kube-system
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func defaults() settings {
	return settings{theme: "default", flash: defaultFlashFor, pane: true, namespace: "kube-system", service: "ovn-topologyd"}
}

func noFlags(string) bool { return false }
func noEnv(string) string { return "" }
func flagSet(names ...string) func(string) bool {
	return func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
}

func TestConfigFillsDefaults(t *testing.T) {
	c, err := loadConfig(writeConfig(t, sampleConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	s := defaults()
	c.apply(&s, "harvester", noFlags, noEnv)
	want := settings{theme: "suse", flash: 30 * time.Second, pane: false, namespace: "ovn-topology", service: "topo",
		port: "8080", k8sNamespace: "kube-system", pinned: true}
	if s != want {
		t.Errorf("got %+v\nwant %+v", s, want)
	}
}

func TestConfigFlagsAndEnvWin(t *testing.T) {
	c, err := loadConfig(writeConfig(t, sampleConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	s := defaults()
	s.theme, s.namespace = "nord", "custom"
	env := func(k string) string {
		if k == "OVN_TOPO_FLASH" {
			return "5s"
		}
		return ""
	}
	s.flash = 5 * time.Second // what the flag default read from the env
	c.apply(&s, "harvester", flagSet("theme", "namespace"), env)
	if s.theme != "nord" || s.namespace != "custom" || s.flash != 5*time.Second {
		t.Errorf("flags and env must beat the file: %+v", s)
	}
	if s.service != "topo" {
		t.Errorf("unset flags still come from the file: %+v", s)
	}
}

func TestConfigUnknownContext(t *testing.T) {
	c, err := loadConfig(writeConfig(t, sampleConfig), true)
	if err != nil {
		t.Fatal(err)
	}
	s := defaults()
	c.apply(&s, "elsewhere", noFlags, noEnv)
	if s.namespace != "kube-system" || s.service != "ovn-topologyd" || s.pinned || s.k8sNamespace != "" {
		t.Errorf("another context must keep the defaults and discovery: %+v", s)
	}
	if s.theme != "suse" {
		t.Errorf("ui settings apply to every context: %+v", s)
	}
}

func TestConfigMissingAndInvalid(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	if _, err := loadConfig(missing, false); err != nil {
		t.Errorf("a missing default file is not an error: %v", err)
	}
	if _, err := loadConfig(missing, true); err == nil {
		t.Errorf("a missing --config file must be an error")
	}
	for name, body := range map[string]string{
		"typo":      "ui:\n  theem: suse\n",
		"diag":      "ui:\n  diag: true\n",
		"bad flash": "ui:\n  flash: soon\n",
	} {
		_, err := loadConfig(writeConfig(t, body), false)
		if err == nil || !strings.Contains(err.Error(), "config") {
			t.Errorf("%s: expected a config error, got %v", name, err)
		}
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := defaultConfigPath(); got != filepath.Join("/xdg", "ovn-topo", "config.yaml") {
		t.Errorf("got %s", got)
	}
}
