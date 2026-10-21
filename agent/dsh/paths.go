package dsh

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectMemoryFile is <work_dir>/AGENTS.md. dsh also reads CLAUDE.md in
// each directory from the project root to the session cwd, plus
// AGENTS.local.md and CLAUDE.local.md overlays. /memory edits this file.
func (a *Agent) ProjectMemoryFile() string {
	return filepath.Join(a.absWorkDir(), "AGENTS.md")
}

// GlobalMemoryFile is $DSH_HOME/AGENTS.md (default ~/.dsh/AGENTS.md).
// dsh also reads $DSH_AGENTS_HOME/AGENTS.md, or ~/.agents/AGENTS.md.
func (a *Agent) GlobalMemoryFile() string {
	home := a.homeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "AGENTS.md")
}

// SkillDirs returns skill roots that exist. dsh scans
// <project>/.dsh/skills, <project>/.agents/skills, $DSH_HOME/skills, and
// $DSH_AGENTS_HOME/skills (default ~/.agents/skills).
func (a *Agent) SkillDirs() []string {
	root := a.absWorkDir()
	candidates := []string{
		filepath.Join(root, ".dsh", "skills"),
		filepath.Join(root, ".agents", "skills"),
	}
	if home := a.homeDir(); home != "" {
		candidates = append(candidates, filepath.Join(home, "skills"))
	}
	if agents := a.agentsHome(); agents != "" {
		candidates = append(candidates, filepath.Join(agents, "skills"))
	}
	return existingDirs(candidates)
}

func (a *Agent) absWorkDir() string {
	dir := ""
	if a != nil && a.Agent != nil {
		dir = a.GetWorkDir()
	}
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && strings.TrimSpace(resolved) != "" {
		return resolved
	}
	return abs
}

func (a *Agent) homeDir() string {
	if a != nil {
		a.mu.Lock()
		opt := strings.TrimSpace(a.dshHome)
		cfg := ""
		if a.configEnv != nil {
			cfg = strings.TrimSpace(a.configEnv["DSH_HOME"])
		}
		a.mu.Unlock()
		if opt != "" {
			return opt
		}
		if cfg != "" {
			return cfg
		}
	}
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		return v
	}
	user, err := os.UserHomeDir()
	if err != nil || user == "" {
		return ""
	}
	return filepath.Join(user, ".dsh")
}

func (a *Agent) agentsHome() string {
	if v := a.envValue("DSH_AGENTS_HOME"); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("DSH_AGENTS_HOME")); v != "" {
		return v
	}
	user, err := os.UserHomeDir()
	if err != nil || user == "" {
		return ""
	}
	return filepath.Join(user, ".agents")
}

func (a *Agent) envValue(key string) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.configEnv == nil {
		return ""
	}
	return strings.TrimSpace(a.configEnv[key])
}

func existingDirs(paths []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
