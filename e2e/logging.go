//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/testcontainers/testcontainers-go"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func StripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

type LogSource string

const (
	SourceStdout LogSource = "stdout"
	SourceStderr LogSource = "stderr"
	SourceTrace  LogSource = "trace"
)

type LogEvent struct {
	Node    string
	Source  LogSource
	Content string
}

type LogSubscription struct {
	Node    string
	Source  LogSource
	Pattern string
	Regex   *regexp.Regexp
	MatchCh chan struct{}
}

type LogManager struct {
	mu          sync.RWMutex
	subscribers []*LogSubscription
	// history keeps track of all logs to allow matching against past logs if needed
	// however, the prompt implies a streaming approach for wait.
	// Let's keep a simple buffer per node/source to allow "WaitFor" to check already received logs.
	history   map[string]map[LogSource]*strings.Builder
	historyMu sync.RWMutex
}

func NewLogManager() *LogManager {
	return &LogManager{
		subscribers: make([]*LogSubscription, 0),
		history:     make(map[string]map[LogSource]*strings.Builder),
	}
}

func (m *LogManager) Accept(node string, source LogSource, content string) {
	m.historyMu.Lock()
	if _, ok := m.history[node]; !ok {
		m.history[node] = make(map[LogSource]*strings.Builder)
	}
	if _, ok := m.history[node][source]; !ok {
		m.history[node][source] = &strings.Builder{}
	}
	m.history[node][source].WriteString(content)
	fullContent := m.history[node][source].String()
	m.historyMu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sub := range m.subscribers {
		if sub.Node != node || sub.Source != source {
			continue
		}
		matched := false
		if sub.Regex != nil {
			if sub.Regex.MatchString(content) || sub.Regex.MatchString(fullContent) {
				matched = true
			}
		} else if sub.Pattern != "" {
			if strings.Contains(content, sub.Pattern) || strings.Contains(fullContent, sub.Pattern) {
				matched = true
			}
		}

		if matched {
			select {
			case sub.MatchCh <- struct{}{}:
			default:
			}
		}
	}
}

func (m *LogManager) Subscribe(node string, source LogSource, pattern string, isRegex bool) (*LogSubscription, error) {
	sub := &LogSubscription{
		Node:    node,
		Source:  source,
		MatchCh: make(chan struct{}, 1),
	}
	if isRegex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		sub.Regex = re
	} else {
		sub.Pattern = pattern
	}

	m.mu.Lock()
	m.subscribers = append(m.subscribers, sub)
	m.mu.Unlock()

	// Check history immediately
	m.historyMu.RLock()
	defer m.historyMu.RUnlock()
	if h, ok := m.history[node]; ok {
		if b, ok := h[source]; ok {
			content := b.String()
			matched := false
			if sub.Regex != nil {
				matched = sub.Regex.MatchString(content)
			} else {
				matched = strings.Contains(content, sub.Pattern)
			}
			if matched {
				sub.MatchCh <- struct{}{}
			}
		}
	}

	return sub, nil
}

func (m *LogManager) Unsubscribe(sub *LogSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subscribers {
		if s == sub {
			m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
			break
		}
	}
}

type UnifiedLogConsumer struct {
	Node    string
	Manager *LogManager
}

func (c *UnifiedLogConsumer) Accept(l testcontainers.Log) {
	source := SourceStdout
	if l.LogType == "stderr" {
		source = SourceStderr
	}
	content := StripAnsi(string(l.Content))
	fmt.Printf("[%s:%s] %s", c.Node, source, content)
	c.Manager.Accept(c.Node, source, content)
}
