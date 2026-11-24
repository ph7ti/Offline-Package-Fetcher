package transfer

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	maxSSHConfigSize       = 1 << 20
	maxSSHConfigIncludeDep = 5
	maxSSHProxyJumpDepth   = 8
)

// SSHConfigEntry holds resolved non-secret connection parameters parsed from
// an OpenSSH client configuration file (~/.ssh/config).
type SSHConfigEntry struct {
	Alias      string
	Host       string
	Port       int
	User       string
	KeyPath    string
	KnownHosts string
	JumpHosts  []SSHConfigEntry
}

type sshConfigBlock struct {
	patterns []string
	values   map[string]string
}

// DefaultSSHConfigPath returns ~/.ssh/config.
func DefaultSSHConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolver diretório home para ~/.ssh/config: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// ResolveSSHConfigAlias resolves an SSH host alias from ~/.ssh/config (or
// configPath when provided), expanding ProxyJump chains with cycle protection.
// It uses OpenSSH first-match-wins semantics.
func ResolveSSHConfigAlias(configPath, alias string) (*SSHConfigEntry, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, errors.New("alias SSH vazio")
	}
	if strings.ContainsAny(alias, " \t\r\n\x00") {
		return nil, fmt.Errorf("alias SSH inválido: %q", alias)
	}
	if strings.TrimSpace(configPath) == "" {
		var err error
		configPath, err = DefaultSSHConfigPath()
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		configPath, err = expandHome(configPath)
		if err != nil {
			return nil, fmt.Errorf("resolver caminho do ssh config: %w", err)
		}
	}

	blocks, err := parseSSHConfigFile(configPath, 0)
	if err != nil {
		return nil, err
	}
	return resolveAliasFromBlocks(blocks, alias, make(map[string]bool), 0)
}

func resolveAliasFromBlocks(blocks []sshConfigBlock, alias string, visiting map[string]bool, depth int) (*SSHConfigEntry, error) {
	if depth > maxSSHProxyJumpDepth {
		return nil, fmt.Errorf("cadeia ProxyJump excede o limite de %d saltos", maxSSHProxyJumpDepth)
	}
	key := strings.ToLower(alias)
	if visiting[key] {
		return nil, fmt.Errorf("ciclo detectado em ProxyJump envolvendo %q", alias)
	}
	visiting[key] = true
	defer delete(visiting, key)

	merged := make(map[string]string)
	for _, block := range blocks {
		if !matchesHostPatterns(alias, block.patterns) {
			continue
		}
		for k, v := range block.values {
			if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
	}

	host := alias
	if v := merged["hostname"]; v != "" {
		host = expandHostTokens(v, alias)
	}
	port := defaultSSHPort
	if v := merged["port"]; v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("porta inválida em ssh config para %q: %q", alias, v)
		}
		port = p
	}
	keyPath := merged["identityfile"]
	if keyPath != "" {
		if expanded, err := expandHome(keyPath); err == nil {
			keyPath = expanded
		}
	}
	knownHosts := merged["userknownhostsfile"]
	if knownHosts != "" {
		first := strings.Fields(knownHosts)[0]
		if first != "" && !strings.EqualFold(first, "none") {
			if expanded, err := expandHome(first); err == nil {
				knownHosts = expanded
			}
		} else {
			knownHosts = ""
		}
	}

	entry := &SSHConfigEntry{
		Alias:      alias,
		Host:       host,
		Port:       port,
		User:       merged["user"],
		KeyPath:    keyPath,
		KnownHosts: knownHosts,
	}

	if proxyJump := strings.TrimSpace(merged["proxyjump"]); proxyJump != "" && !strings.EqualFold(proxyJump, "none") {
		hops := strings.Split(proxyJump, ",")
		for _, rawHop := range hops {
			rawHop = strings.TrimSpace(rawHop)
			if rawHop == "" {
				continue
			}
			hopUser, hopHost, hopPort, err := splitProxyJumpToken(rawHop)
			if err != nil {
				return nil, fmt.Errorf("ProxyJump inválido %q em %q: %w", rawHop, alias, err)
			}
			resolvedHop, err := resolveAliasFromBlocks(blocks, hopHost, visiting, depth+1)
			if err != nil {
				return nil, err
			}
			// Append any nested jump hops first (preserving hop order).
			if len(resolvedHop.JumpHosts) > 0 {
				entry.JumpHosts = append(entry.JumpHosts, resolvedHop.JumpHosts...)
				resolvedHop.JumpHosts = nil
			}
			if hopUser != "" {
				resolvedHop.User = hopUser
			}
			if hopPort > 0 {
				resolvedHop.Port = hopPort
			}
			entry.JumpHosts = append(entry.JumpHosts, *resolvedHop)
		}
	}

	return entry, nil
}

func parseSSHConfigFile(path string, depth int) ([]sshConfigBlock, error) {
	if depth > maxSSHConfigIncludeDep {
		return nil, fmt.Errorf("profundidade de Include em ssh config excede %d", maxSSHConfigIncludeDep)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("ler ssh config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ssh config %q não é arquivo regular", path)
	}
	if info.Size() > maxSSHConfigSize {
		return nil, fmt.Errorf("ssh config %q excede %d bytes", path, maxSSHConfigSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("abrir ssh config %q: %w", path, err)
	}
	defer f.Close()

	baseDir := filepath.Dir(path)
	var blocks []sshConfigBlock
	current := sshConfigBlock{patterns: []string{"*"}, values: make(map[string]string)}
	hasGlobal := false

	flushCurrent := func() {
		if len(current.values) > 0 {
			blocks = append(blocks, current)
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keyword, value := splitSSHConfigDirective(line)
		if keyword == "" || value == "" {
			continue
		}
		lowerKey := strings.ToLower(keyword)
		switch lowerKey {
		case "host":
			if hasGlobal || len(current.values) > 0 {
				flushCurrent()
			}
			current = sshConfigBlock{
				patterns: strings.Fields(value),
				values:   make(map[string]string),
			}
			hasGlobal = true
		case "match":
			// Match blocks are skipped; start a dummy non-matching block so
			// directives inside Match do not leak into the previous Host block.
			if hasGlobal || len(current.values) > 0 {
				flushCurrent()
			}
			current = sshConfigBlock{patterns: nil, values: make(map[string]string)}
			hasGlobal = true
		case "include":
			for _, pattern := range strings.Fields(value) {
				if expanded, expErr := expandHome(pattern); expErr == nil {
					pattern = expanded
				}
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(baseDir, pattern)
				}
				matches, globErr := filepath.Glob(pattern)
				if globErr != nil {
					continue
				}
				for _, match := range matches {
					subBlocks, subErr := parseSSHConfigFile(match, depth+1)
					if subErr != nil {
						return nil, subErr
					}
					blocks = append(blocks, subBlocks...)
				}
			}
		default:
			if _, exists := current.values[lowerKey]; !exists {
				current.values[lowerKey] = strings.Trim(value, `"`)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ler ssh config %q: %w", path, err)
	}
	flushCurrent()
	return blocks, nil
}

func splitSSHConfigDirective(line string) (string, string) {
	// OpenSSH allows either whitespace or '=' between keyword and value.
	i := 0
	for i < len(line) && line[i] != ' ' && line[i] != '\t' && line[i] != '=' {
		i++
	}
	key := strings.TrimSpace(line[:i])
	rest := strings.TrimSpace(line[i:])
	rest = strings.TrimPrefix(rest, "=")
	return key, strings.TrimSpace(rest)
}

func matchesHostPatterns(alias string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	matched := false
	for _, pat := range patterns {
		negated := false
		if strings.HasPrefix(pat, "!") {
			negated = true
			pat = pat[1:]
		}
		if wildcardMatch(pat, alias) {
			if negated {
				return false
			}
			matched = true
		}
	}
	return matched
}

func wildcardMatch(pattern, s string) bool {
	pi, si := 0, 0
	starIdx, matchIdx := -1, 0
	for si < len(s) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == s[si]) {
			pi++
			si++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			starIdx = pi
			matchIdx = si
			pi++
		} else if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			si = matchIdx
		} else {
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

func expandHostTokens(value, alias string) string {
	return strings.ReplaceAll(value, "%h", alias)
}

func splitProxyJumpToken(spec string) (user, host string, port int, err error) {
	if at := strings.LastIndex(spec, "@"); at >= 0 {
		user = spec[:at]
		spec = spec[at+1:]
		if user == "" {
			return "", "", 0, errors.New("usuário vazio antes de @")
		}
	}
	if spec == "" {
		return "", "", 0, errors.New("host vazio")
	}
	if strings.HasPrefix(spec, "[") || strings.Count(spec, ":") == 1 {
		h, pStr, splitErr := net.SplitHostPort(spec)
		if splitErr != nil {
			return "", "", 0, splitErr
		}
		p, pErr := strconv.Atoi(pStr)
		if pErr != nil || p <= 0 || p > 65535 {
			return "", "", 0, fmt.Errorf("porta inválida: %q", pStr)
		}
		return user, h, p, nil
	}
	return user, spec, 0, nil
}
