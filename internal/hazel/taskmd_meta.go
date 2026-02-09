package hazel

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Task metadata is stored inside task.md.
// New format: a hidden-at-render-time config block at the bottom:
//
//   <!-- HAZEL-CONFIG
//   hazel:
//     color: cloud
//     priority: HIGH
//   HAZEL-CONFIG -->
//
// This repo is pre-launch; no backwards compatibility is maintained.

type taskFrontmatter struct {
	Hazel taskHazelMeta `yaml:"hazel"`
}

type taskHazelMeta struct {
	Color    string `yaml:"color"`
	Priority string `yaml:"priority"`
}

var pastelPalette = []struct {
	Key string
	Hex string
}{
	{"peach", "#ffe0d2"},
	{"mint", "#d9ffe9"},
	{"sky", "#d8ecff"},
	{"lilac", "#efe1ff"},
	{"lemon", "#fff7c9"},
	{"blush", "#ffd8e9"},
	{"sage", "#e5ffd8"},
	{"sand", "#fff0d8"},
	{"ice", "#d8fff8"},
	{"cloud", "#e8eefc"},
}

func colorHexForKey(key string) string {
	for _, p := range pastelPalette {
		if p.Key == key {
			return p.Hex
		}
	}
	return pastelPalette[0].Hex
}

func defaultColorKeyForID(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	idx := int(h.Sum32()) % len(pastelPalette)
	return pastelPalette[idx].Key
}

func randomColorKey() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		idx := int(binary.LittleEndian.Uint64(b[:]) % uint64(len(pastelPalette)))
		return pastelPalette[idx].Key
	}
	return pastelPalette[0].Key
}

func readTaskMD(root, id string) (string, error) {
	b, err := os.ReadFile(taskFile(root, id, "task.md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeTaskMD(root, id string, content string) error {
	return writeFileAtomic(taskFile(root, id, "task.md"), []byte(content), 0o644)
}

const hazelCfgStart = "<!-- HAZEL-CONFIG"
const hazelCfgEnd = "HAZEL-CONFIG -->"

func parseHazelConfigBlock(md string) (cfg taskFrontmatter, has bool, without string, err error) {
	i := strings.LastIndex(md, hazelCfgStart)
	if i < 0 {
		return taskFrontmatter{}, false, md, nil
	}
	rest := md[i:]
	j := strings.Index(rest, hazelCfgEnd)
	if j < 0 {
		return taskFrontmatter{}, false, md, nil
	}

	block := rest[:j+len(hazelCfgEnd)]
	// YAML begins after the first newline following start marker, if present.
	y := strings.TrimPrefix(block, hazelCfgStart)
	y = strings.TrimPrefix(y, "\r")
	y = strings.TrimSpace(y)
	if strings.HasPrefix(y, "-->") {
		return taskFrontmatter{}, false, md, nil
	}
	if strings.HasPrefix(rest, hazelCfgStart+"\n") {
		y = rest[len(hazelCfgStart)+1 : j]
	} else {
		// Require newline after marker for predictability.
		return taskFrontmatter{}, false, md, nil
	}
	y = strings.TrimSpace(y)

	dec := yaml.NewDecoder(strings.NewReader(y))
	if err := dec.Decode(&cfg); err != nil {
		return taskFrontmatter{}, false, md, fmt.Errorf("task config block: %w", err)
	}

	before := md[:i]
	after := md[i+len(block):]
	without = before + after
	return cfg, true, without, nil
}

func formatHazelConfigBlock(cfg taskFrontmatter) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&cfg); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	y := strings.TrimRight(buf.String(), "\n") + "\n"
	return "\n" + hazelCfgStart + "\n" + y + hazelCfgEnd + "\n", nil
}

func getTaskConfig(md string) (cfg taskHazelMeta, ok bool) {
	if fm, has, _, err := parseHazelConfigBlock(md); err == nil && has {
		return fm.Hazel, true
	}
	return taskHazelMeta{}, false
}

func stripTaskConfigForRender(md string) (string, error) {
	if _, has, without, err := parseHazelConfigBlock(md); err != nil {
		return md, err
	} else if has {
		return without, nil
	}
	return md, nil
}

func getTaskColorFromMD(md string) (string, bool) {
	cfg, ok := getTaskConfig(md)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(cfg.Color) == "" {
		return "", false
	}
	return strings.TrimSpace(cfg.Color), true
}

func getTaskPriorityFromMD(md string) (string, bool) {
	cfg, ok := getTaskConfig(md)
	if !ok {
		return "", false
	}
	lbl := strings.ToUpper(strings.TrimSpace(cfg.Priority))
	if lbl == "" {
		return "", false
	}
	switch lbl {
	case "HIGH", "MEDIUM", "LOW":
		return lbl, true
	default:
		return "", false
	}
}

func setTaskColorInMD(md string, colorKey string) (string, error) {
	if strings.TrimSpace(colorKey) == "" {
		colorKey = pastelPalette[0].Key
	}

	// Prefer updating existing config block (always re-append at bottom).
	fm, has, without, err := parseHazelConfigBlock(md)
	if err != nil {
		return "", err
	}
	if has {
		fm.Hazel.Color = colorKey
		block, err := formatHazelConfigBlock(fm)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(without, " \t\r\n") + "\n" + block, nil
	}

	// Otherwise, append fresh block.
	newFM := taskFrontmatter{Hazel: taskHazelMeta{Color: colorKey}}
	block, err := formatHazelConfigBlock(newFM)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(md, " \t\r\n") + "\n" + block, nil
}

func setTaskPriorityInMD(md string, priorityLabel string) (string, error) {
	lbl := strings.ToUpper(strings.TrimSpace(priorityLabel))
	if lbl != "" && lbl != "HIGH" && lbl != "MEDIUM" && lbl != "LOW" {
		return "", fmt.Errorf("invalid priority %q", priorityLabel)
	}

	fm, has, without, err := parseHazelConfigBlock(md)
	if err != nil {
		return "", err
	}
	if has {
		fm.Hazel.Priority = lbl
		block, err := formatHazelConfigBlock(fm)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(without, " \t\r\n") + "\n" + block, nil
	}

	newFM := taskFrontmatter{Hazel: taskHazelMeta{Priority: lbl}}
	block, err := formatHazelConfigBlock(newFM)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(md, " \t\r\n") + "\n" + block, nil
}

func ensureTaskColor(root, id string, defaultKey string) error {
	md, err := readTaskMD(root, id)
	if err != nil {
		return err
	}
	if _, ok := getTaskColorFromMD(md); ok {
		return nil
	}
	updated, err := setTaskColorInMD(md, defaultKey)
	if err != nil {
		return err
	}
	return writeTaskMD(root, id, updated)
}
