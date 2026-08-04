package process

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvInputs are the routeup-derived values injected into the child environment.
type EnvInputs struct {
	// Port is the assigned or configured app port, exported as PORT.
	Port int
	// Host is exported as HOST. Empty defaults to 127.0.0.1.
	Host string
	// LocalURL is exported as ROUTEUP_LOCAL_URL. Also sets ROUTEUP_URL unless
	// PublicURL is provided.
	LocalURL string
	// PublicURL, when non-empty, sets ROUTEUP_URL to the granted public URL.
	// ROUTEUP_LOCAL_URL always contains the local URL.
	PublicURL string
	// CACertPath, when non-empty, is exported as NODE_EXTRA_CA_CERTS so
	// Node.js child processes trust the routeup local CA without manual setup.
	CACertPath string
	// WorkDir is the project directory; its node_modules/.bin is prepended to
	// PATH so local dev binaries resolve without a package-manager wrapper.
	WorkDir string
}

// InjectEnv returns base with routeup's variables applied: PORT, HOST,
// ROUTEUP_LOCAL_URL, ROUTEUP_URL, and WorkDir/node_modules/.bin prepended to
// PATH. Existing entries for those keys are replaced in place (no duplicates),
// and unrelated variables are preserved. Pass os.Environ() as base to inherit
// the current environment.
func InjectEnv(base []string, in EnvInputs) []string {
	env := make([]string, len(base))
	copy(env, base)

	if in.WorkDir != "" {
		env = prependPath(env, filepath.Join(in.WorkDir, "node_modules", ".bin"))
	}
	if in.Port > 0 {
		env = upsert(env, "PORT", strconv.Itoa(in.Port))
	}
	host := in.Host
	if host == "" {
		host = "127.0.0.1"
	}
	env = upsert(env, "HOST", host)
	if in.LocalURL != "" {
		env = upsert(env, "ROUTEUP_LOCAL_URL", in.LocalURL)
		routeupURL := in.LocalURL
		if in.PublicURL != "" {
			routeupURL = in.PublicURL
		}
		env = upsert(env, "ROUTEUP_URL", routeupURL)
	}
	if in.CACertPath != "" {
		env = upsert(env, "NODE_EXTRA_CA_CERTS", in.CACertPath)
	}
	return env
}

func upsert(env []string, key, val string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

func prependPath(env []string, dir string) []string {
	const key = "PATH="
	for i, kv := range env {
		if !strings.HasPrefix(kv, key) {
			continue
		}
		existing := strings.TrimPrefix(kv, key)
		if existing == "" {
			env[i] = key + dir
			return env
		}
		env[i] = key + dir + string(os.PathListSeparator) + existing
		return env
	}
	return append(env, key+dir)
}
