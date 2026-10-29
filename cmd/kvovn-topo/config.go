package main

// The optional config file, ~/.config/kvovn-topo/config.yaml: UI preferences
// and per-kubeconfig-context connection settings, so they need not be retyped.
// A flag beats its environment variable, which beats the file. --diag, --zone
// and --fixture-dir are per run and stay out of it (ADR-0001: Tier 1 is an
// explicit opt-in).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"sigs.k8s.io/yaml"
)

type config struct {
	UI struct {
		Theme string `json:"theme,omitempty"`
		Flash string `json:"flash,omitempty"`
		Pane  *bool  `json:"pane,omitempty"`
	} `json:"ui"`
	Contexts map[string]contextConfig `json:"contexts,omitempty"`
}

type contextConfig struct {
	Daemon struct {
		Namespace string `json:"namespace,omitempty"`
		Service   string `json:"service,omitempty"`
		Port      string `json:"port,omitempty"`
	} `json:"daemon"`
	K8sNamespace string `json:"k8sNamespace,omitempty"`
}

// settings are the values the file can provide, after flags and env.
type settings struct {
	theme                    string
	flash                    time.Duration
	pane                     bool
	namespace, service, port string
	k8sNamespace             string
	pinned                   bool // the daemon Service was named: skip discovery
}

// defaultConfigPath is $XDG_CONFIG_HOME/kvovn-topo/config.yaml, else
// ~/.config/kvovn-topo/config.yaml (%AppData% on Windows).
func defaultConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" && runtime.GOOS == "windows" {
		dir, _ = os.UserConfigDir()
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "kvovn-topo", "config.yaml")
}

// loadConfig reads path. A missing default file is an empty config; a file
// named with --config must exist.
func loadConfig(path string, explicit bool) (*config, error) {
	c := &config{}
	if path == "" {
		return c, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := yaml.UnmarshalStrict(raw, c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if c.UI.Flash != "" {
		if _, err := time.ParseDuration(c.UI.Flash); err != nil {
			return nil, fmt.Errorf("config %s: ui.flash: %w", path, err)
		}
	}
	return c, nil
}

// apply fills s from the file where no flag (set) or environment variable
// (env) gave a value. kubeCtx selects the contexts entry.
func (c *config) apply(s *settings, kubeCtx string, set func(flag string) bool, env func(string) string) {
	if c.UI.Theme != "" && !set("theme") && env("THEME") == "" {
		s.theme = c.UI.Theme
	}
	if c.UI.Flash != "" && !set("flash") && env("KVOVN_TOPO_FLASH") == "" {
		s.flash, _ = time.ParseDuration(c.UI.Flash) // validated by loadConfig
	}
	if c.UI.Pane != nil {
		s.pane = *c.UI.Pane
	}
	cc, ok := c.Contexts[kubeCtx]
	if !ok {
		return
	}
	if cc.Daemon.Namespace != "" && !set("namespace") {
		s.namespace, s.pinned = cc.Daemon.Namespace, true
	}
	if cc.Daemon.Service != "" && !set("service") {
		s.service, s.pinned = cc.Daemon.Service, true
	}
	if cc.Daemon.Port != "" && !set("port") {
		s.port = cc.Daemon.Port
	}
	if cc.K8sNamespace != "" && !set("k8s-namespace") {
		s.k8sNamespace = cc.K8sNamespace
	}
}
