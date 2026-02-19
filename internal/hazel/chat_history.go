package hazel

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type chatSessionSummary struct {
	Name          string
	TaskID        string
	Path          string
	ModifiedAt    time.Time
	EventCount    int
	LastAssistant string
}

func chatSessionsDir(root string) string {
	return filepath.Join(hazelDir(root), "chat", "sessions")
}

func taskIDFromChatSessionName(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), ".jsonl")
	if !strings.HasPrefix(base, "20") {
		return ""
	}
	parts := strings.Split(base, "_")
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func loadChatSessionEvents(path string) ([]codexEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []codexEvent
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func listChatSessionSummaries(root string) ([]chatSessionSummary, error) {
	dir := chatSessionsDir(root)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []chatSessionSummary{}, nil
		}
		return nil, err
	}
	out := make([]chatSessionSummary, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		evs, _ := loadChatSessionEvents(p)
		lastAssistant := ""
		for i := len(evs) - 1; i >= 0; i-- {
			if evs[i].Type == "assistant_message" && strings.TrimSpace(evs[i].Text) != "" {
				lastAssistant = strings.TrimSpace(evs[i].Text)
				break
			}
		}
		out = append(out, chatSessionSummary{
			Name:          e.Name(),
			TaskID:        taskIDFromChatSessionName(e.Name()),
			Path:          p,
			ModifiedAt:    st.ModTime(),
			EventCount:    len(evs),
			LastAssistant: lastAssistant,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedAt.After(out[j].ModifiedAt)
	})
	return out, nil
}

func latestChatSessionForTask(root string, taskID string) (chatSessionSummary, bool) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return chatSessionSummary{}, false
	}
	all, err := listChatSessionSummaries(root)
	if err != nil {
		return chatSessionSummary{}, false
	}
	for _, s := range all {
		if s.TaskID == taskID {
			return s, true
		}
	}
	return chatSessionSummary{}, false
}
