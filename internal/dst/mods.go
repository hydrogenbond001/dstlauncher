package dst

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ModOverridesFile = "modoverrides.lua"
	ModSetupFile     = "dedicated_server_mods_setup.lua"
)

type ModEntry struct {
	ID         string
	Enabled    bool
	Raw        string
	KeyStart   int
	ValueStart int
	ValueEnd   int
}

var setupRe = regexp.MustCompile(`^ServerModSetup\s*\(\s*["']([0-9]+)["']\s*\)`)
var modNameRe = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)

// ParseModOverrides extracts top-level workshop entries from modoverrides.lua
// while keeping each entry's value block verbatim.
func ParseModOverrides(data []byte) ([]ModEntry, int, int, error) {
	s := string(data)
	open, close, err := findReturnTable(s)
	if err != nil {
		return nil, 0, 0, err
	}
	var entries []ModEntry
	i := open + 1
	for i < close {
		i = skipTrivia(s, i)
		if i >= close {
			break
		}
		if s[i] != '[' {
			i++
			continue
		}
		keyStart := i
		j := skipTrivia(s, i+1)
		if j >= len(s) || (s[j] != '"' && s[j] != '\'') {
			i++
			continue
		}
		keyEnd := skipString(s, j)
		key := s[j+1 : keyEnd-1]
		id, ok := workshopID(key)
		if !ok {
			i = keyEnd
			continue
		}
		k := skipTrivia(s, keyEnd)
		if k >= len(s) || s[k] != ']' {
			i = keyEnd
			continue
		}
		k = skipTrivia(s, k+1)
		if k >= len(s) || s[k] != '=' {
			i = keyEnd
			continue
		}
		k = skipTrivia(s, k+1)
		if k >= len(s) || s[k] != '{' {
			i = keyEnd
			continue
		}
		valueEnd := matchBrace(s, k)
		if valueEnd < 0 {
			return nil, 0, 0, fmt.Errorf("modoverrides.lua 第 %d 字节附近的大括号不匹配", k)
		}
		entries = append(entries, ModEntry{
			ID:         id,
			Enabled:    enabledInBlock(s[k : valueEnd+1]),
			Raw:        s[k : valueEnd+1],
			KeyStart:   keyStart,
			ValueStart: k,
			ValueEnd:   valueEnd,
		})
		i = valueEnd + 1
	}
	return entries, open, close, nil
}

// EnsureMod adds the mod if missing; existing entries only get enabled updated.
func EnsureMod(data []byte, id string, enabled bool) ([]byte, error) {
	entries, open, _, err := ParseModOverrides(data)
	if err == nil {
		for _, e := range entries {
			if e.ID == id {
				return SetModEnabled(data, id, enabled)
			}
		}
		entry := fmt.Sprintf("\n  [\"workshop-%s\"] = { enabled = %t },", id, enabled)
		out := string(data)
		return []byte(out[:open+1] + entry + out[open+1:]), nil
	}
	// Missing/empty file: start from a valid empty table.
	base := "return {\n}\n"
	open, _, err2 := findReturnTable(base)
	if err2 != nil {
		return nil, err2
	}
	entry := fmt.Sprintf("\n  [\"workshop-%s\"] = { enabled = %t },", id, enabled)
	return []byte(base[:open+1] + entry + base[open+1:]), nil
}

func SetModEnabled(data []byte, id string, enabled bool) ([]byte, error) {
	entries, _, _, err := ParseModOverrides(data)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		raw := e.Raw
		start, end, ok := enabledSpan(raw)
		var newRaw string
		if ok {
			newRaw = raw[:start] + strconv.FormatBool(enabled) + raw[end:]
		} else {
			newRaw = raw[:1] + fmt.Sprintf(" enabled = %t,", enabled) + raw[1:]
		}
		return []byte(string(data[:e.ValueStart]) + newRaw + string(data[e.ValueEnd+1:])), nil
	}
	return nil, fmt.Errorf("mod %s 不在 modoverrides.lua 中", id)
}

func RemoveMod(data []byte, id string) ([]byte, error) {
	entries, _, _, err := ParseModOverrides(data)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		start, end := e.KeyStart, e.ValueEnd+1
		j := skipTrivia(string(data), end)
		if j < len(data) && data[j] == ',' {
			end = j + 1
		} else {
			k := start - 1
			for k >= 0 && (data[k] == ' ' || data[k] == '\t' || data[k] == '\n' || data[k] == '\r') {
				k--
			}
			if k >= 0 && data[k] == ',' {
				start = k
			}
		}
		out := append([]byte{}, data[:start]...)
		out = append(out, data[end:]...)
		return out, nil
	}
	return nil, fmt.Errorf("mod %s 不在 modoverrides.lua 中", id)
}

// SetupIDs lists ServerModSetup("id") entries, ignoring commented lines.
func SetupIDs(data []byte) map[string]bool {
	out := map[string]bool{}
	for _, line := range splitLines(string(data)) {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "--") {
			continue
		}
		if m := setupRe.FindStringSubmatch(trim); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

func AddSetup(data []byte, id, name string) []byte {
	out := string(data)
	eol := detectEOL(out)
	if SetupIDs(data)[id] {
		return data
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += eol
	}
	comment := ""
	if name = strings.TrimSpace(name); name != "" {
		comment = " -- " + strings.ReplaceAll(name, "\n", " ")
	}
	out += fmt.Sprintf("ServerModSetup(%q)%s%s", id, comment, eol)
	return []byte(out)
}

func RemoveSetup(data []byte, id string) []byte {
	var kept []string
	removed := false
	for _, line := range splitLines(string(data)) {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "--") {
			if m := setupRe.FindStringSubmatch(trim); m != nil && m[1] == id {
				removed = true
				continue
			}
		}
		kept = append(kept, line)
	}
	if !removed {
		return data
	}
	return []byte(strings.Join(kept, detectEOL(string(data))))
}

// WriteFileBackup backs up the existing file once per call, then writes
// atomically. Unchanged content is left alone.
func WriteFileBackup(path string, data []byte) error {
	old, err := os.ReadFile(path)
	if err == nil && string(old) == string(data) {
		return nil
	}
	if err == nil {
		backup := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0o644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mod-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ModName(installRoot, id string) string {
	path := filepath.Join(installRoot, "mods", "workshop-"+id, "modinfo.lua")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := modNameRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

func workshopID(key string) (string, bool) {
	const prefix = "workshop-"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	id := key[len(prefix):]
	if id == "" {
		return "", false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return id, true
}

func findReturnTable(s string) (int, int, error) {
	i := 0
	for i < len(s) {
		i = skipTrivia(s, i)
		if i >= len(s) {
			break
		}
		if strings.HasPrefix(s[i:], "return") {
			j := skipTrivia(s, i+len("return"))
			if j < len(s) && s[j] == '{' {
				end := matchBrace(s, j)
				if end < 0 {
					return 0, 0, fmt.Errorf("modoverrides.lua 的大括号不匹配")
				}
				return j, end, nil
			}
		}
		if s[i] == '"' || s[i] == '\'' {
			i = skipString(s, i)
			continue
		}
		if s[i] == '[' {
			if j, ok := skipLongBracket(s, i); ok {
				i = j
				continue
			}
		}
		i++
	}
	return 0, 0, fmt.Errorf("modoverrides.lua 找不到 return { ... }")
}

func enabledInBlock(raw string) bool {
	if len(raw) < 2 {
		return false
	}
	i := 1
	end := len(raw) - 1
	for i < end {
		i = skipTrivia(raw, i)
		if i >= end {
			break
		}
		switch raw[i] {
		case '{':
			next := matchBrace(raw, i)
			if next < 0 {
				return false
			}
			i = next + 1
			continue
		case '"', '\'':
			i = skipString(raw, i)
			continue
		case '[':
			if j, ok := skipLongBracket(raw, i); ok {
				i = j
				continue
			}
		}
		if strings.HasPrefix(raw[i:], "enabled") && wordBoundary(raw, i+len("enabled")) {
			j := skipTrivia(raw, i+len("enabled"))
			if j < end && raw[j] == '=' {
				j = skipTrivia(raw, j+1)
				if strings.HasPrefix(raw[j:], "true") {
					return true
				}
				if strings.HasPrefix(raw[j:], "false") {
					return false
				}
			}
		}
		i++
	}
	return false
}

func enabledSpan(raw string) (int, int, bool) {
	if len(raw) < 2 {
		return 0, 0, false
	}
	i := 1
	end := len(raw) - 1
	for i < end {
		i = skipTrivia(raw, i)
		if i >= end {
			break
		}
		switch raw[i] {
		case '{':
			next := matchBrace(raw, i)
			if next < 0 {
				return 0, 0, false
			}
			i = next + 1
			continue
		case '"', '\'':
			i = skipString(raw, i)
			continue
		case '[':
			if j, ok := skipLongBracket(raw, i); ok {
				i = j
				continue
			}
		}
		if strings.HasPrefix(raw[i:], "enabled") && wordBoundary(raw, i+len("enabled")) {
			j := skipTrivia(raw, i+len("enabled"))
			if j < end && raw[j] == '=' {
				j = skipTrivia(raw, j+1)
				if strings.HasPrefix(raw[j:], "true") {
					return j, j + 4, true
				}
				if strings.HasPrefix(raw[j:], "false") {
					return j, j + 5, true
				}
			}
		}
		i++
	}
	return 0, 0, false
}

func skipTrivia(s string, i int) int {
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if c == '-' && i+1 < len(s) && s[i+1] == '-' {
			if j, ok := skipLongBracket(s, i+2); ok {
				i = j
				continue
			}
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		break
	}
	return i
}

func skipString(s string, i int) int {
	q := s[i]
	i++
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == q {
			return i + 1
		}
		i++
	}
	return i
}

func skipLongBracket(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '[' {
		return i, false
	}
	j := i + 1
	eq := 0
	for j < len(s) && s[j] == '=' {
		eq++
		j++
	}
	if j >= len(s) || s[j] != '[' {
		return i, false
	}
	j++
	closing := "]" + strings.Repeat("=", eq) + "]"
	if k := strings.Index(s[j:], closing); k >= 0 {
		return j + k + len(closing), true
	}
	return len(s), true
}

func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		i = skipTrivia(s, i)
		if i >= len(s) {
			return -1
		}
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		case '"', '\'':
			i = skipString(s, i)
			continue
		case '[':
			if j, ok := skipLongBracket(s, i); ok {
				i = j
				continue
			}
		}
		i++
	}
	return -1
}

func wordBoundary(s string, i int) bool {
	return i >= len(s) || !(s[i] == '_' || (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= '0' && s[i] <= '9'))
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func detectEOL(s string) string {
	if strings.Contains(s, "\r\n") {
		return "\r\n"
	}
	return "\n"
}
