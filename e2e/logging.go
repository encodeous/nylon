//go:build e2e

package e2e

import (
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

type sourceKey struct {
	node   string
	source LogSource
}

type LogManager struct {
	mu          sync.Mutex
	subscribers map[sourceKey]*LogSubscription
	histories   map[sourceKey][]string
}

func NewLogManager() *LogManager {
	return &LogManager{
		subscribers: make(map[sourceKey]*LogSubscription),
		histories:   make(map[sourceKey][]string),
	}
}

func (m *LogManager) Accept(node string, source LogSource, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sourceKey{node, source}
	m.histories[key] = append(m.histories[key], content)
	m.checkMatchLocked(key)
}

func (m *LogManager) checkMatchLocked(key sourceKey) {
	sub, ok := m.subscribers[key]
	if !ok {
		return
	}

	history := m.histories[key]
	for i, content := range history {
		matched := false
		if sub.Regex != nil {
			if sub.Regex.MatchString(content) {
				matched = true
			}
		} else if sub.Pattern != "" {
			if strings.Contains(content, sub.Pattern) {
				matched = true
			}
		}

		if matched {
			m.histories[key] = history[i+1:]
			select {
			case sub.MatchCh <- struct{}{}:
			default:
			}
			return
		}
	}
}

func (m *LogManager) Subscribe(node string, source LogSource, pattern string, isRegex bool) (*LogSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sourceKey{node, source}
	if _, ok := m.subscribers[key]; ok {
		return nil, fmt.Errorf("node %s source %s already has a subscriber", node, source)
	}

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

	m.subscribers[key] = sub
	m.checkMatchLocked(key)

	return sub, nil
}

func (m *LogManager) Unsubscribe(sub *LogSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := sourceKey{sub.Node, sub.Source}
	if current, ok := m.subscribers[key]; ok && current == sub {
		delete(m.subscribers, key)
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
