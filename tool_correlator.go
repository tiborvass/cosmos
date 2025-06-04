package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ToolCompletionEvent represents when a tool finishes execution
type ToolCompletionEvent struct {
	ToolID    string
	Timestamp time.Time
	LineNo    int
}

// LogEntry represents Claude project log structure
// Expected structure from ~/.claude/projects/-Users-home-cosmos/*.jsonl:
// {"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_123","type":"tool_result","content":"..."}]},"timestamp":"..."}
// or
// {"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_123","name":"Read","input":{"file_path":"..."}}]},"timestamp":"..."}
type LogEntry struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	Timestamp time.Time `json:"timestamp"`
	Message   *Message  `json:"message,omitempty"`
}

// MessageContent represents content within a message
// For tool_use: {"type":"tool_use","id":"toolu_123","name":"Read","input":{...}}
// For tool_result: {"type":"tool_result","tool_use_id":"toolu_123","content":"..."}
type MessageContent struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`          // For tool_use entries
	ToolUseID string `json:"tool_use_id,omitempty"` // For tool_result entries
	Name      string `json:"name,omitempty"`        // Tool name for tool_use
	Content   string `json:"content,omitempty"`     // Result content for tool_result
}

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content,omitempty"` // Can be string or []MessageContent
}

// ToolCompletionCorrelator efficiently streams and detects tool completions
type ToolCompletionCorrelator struct {
	pendingToolIDs map[string]struct{}
	m              sync.RWMutex
	onCompletion   func(ToolCompletionEvent)
}

/////////
///////// runningtools
/////////

// NewToolCompletionCorrelator creates a correlator with known pending tool IDs
func NewToolCompletionCorrelator(pendingToolIDs map[string]struct{}, onCompletion func(ToolCompletionEvent)) *ToolCompletionCorrelator {
	// Copy map to avoid race conditions
	pending := make(map[string]struct{})
	if pendingToolIDs != nil {
		for id := range pendingToolIDs {
			pending[id] = struct{}{}
		}
	}

	return &ToolCompletionCorrelator{
		pendingToolIDs: pending,
		onCompletion:   onCompletion,
	}
}

// AddPendingTool adds a tool ID to track for completion
func (tc *ToolCompletionCorrelator) AddPendingTool(toolID string) {
	if tc == nil || toolID == "" {
		return
	}
	tc.m.Lock()
	defer tc.m.Unlock()
	tc.pendingToolIDs[toolID] = struct{}{}
}

// StreamAndDetectCompletions efficiently parses JSONL stream to detect tool completions
func (tc *ToolCompletionCorrelator) StreamAndDetectCompletions(reader io.Reader) error {
	if tc == nil {
		return fmt.Errorf("nil correlator")
	}
	if reader == nil {
		return fmt.Errorf("nil reader")
	}

	decoder := json.NewDecoder(reader)
	lineNo := 0
	var lastError error

	for {
		var entry LogEntry
		if err := decoder.Decode(&entry); err != nil {
			if err == io.EOF {
				break
			}
			// Log error but continue processing if possible
			fmt.Fprintf(os.Stderr, "WARNING: Failed to decode JSON at line %d: %v\n", lineNo+1, err)
			lastError = err
			// Skip malformed JSON and continue
			continue
		}

		lineNo++

		// Check if this is a tool_result entry (indicates tool completion)
		if entry.Type == "user" && entry.Message != nil && entry.Message.Role == "user" {
			// Content can be string or []interface{}, we only care about arrays
			if contentArray, ok := entry.Message.Content.([]interface{}); ok {
				tc.checkForToolResults(contentArray, entry.Timestamp, lineNo)
			}
		}
	}

	if lastError != nil {
		return fmt.Errorf("stream processing completed with errors, last error: %w", lastError)
	}
	return nil
}

// checkForToolResults looks for tool_result content indicating tool completion
func (tc *ToolCompletionCorrelator) checkForToolResults(content []interface{}, timestamp time.Time, lineNo int) {
	if tc == nil || content == nil {
		return
	}

	for _, item := range content {
		if item == nil {
			continue
		}

		// Convert interface{} to map to access fields
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this is a tool_result type
		itemType, exists := itemMap["type"]
		if !exists || itemType != "tool_result" {
			continue
		}

		// Extract tool_use_id
		toolUseID, exists := itemMap["tool_use_id"]
		if !exists {
			continue
		}

		toolIDStr, ok := toolUseID.(string)
		if !ok || toolIDStr == "" {
			continue
		}

		// Check if we're tracking this tool
		if !tc.isPendingTool(toolIDStr) {
			continue
		}

		// Tool has completed
		tc.markToolCompleted(toolIDStr)

		if tc.onCompletion != nil {
			// Recover from panic in callback
			func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "ERROR: Panic in completion callback for tool %s: %v\n", toolIDStr, r)
					}
				}()
				tc.onCompletion(ToolCompletionEvent{
					ToolID:    toolIDStr,
					Timestamp: timestamp,
					LineNo:    lineNo,
				})
			}()
		}
	}
}

// isPendingTool checks if a tool ID is pending completion
func (tc *ToolCompletionCorrelator) isPendingTool(toolID string) bool {
	if tc == nil || toolID == "" {
		return false
	}
	tc.m.RLock()
	defer tc.m.RUnlock()
	_, exists := tc.pendingToolIDs[toolID]
	return exists
}

// markToolCompleted removes tool from pending list
func (tc *ToolCompletionCorrelator) markToolCompleted(toolID string) {
	if tc == nil || toolID == "" {
		return
	}
	tc.m.Lock()
	defer tc.m.Unlock()
	delete(tc.pendingToolIDs, toolID)
}

// GetPendingTools returns copy of currently pending tool IDs
func (tc *ToolCompletionCorrelator) GetPendingTools() map[string]struct{} {
	if tc == nil {
		return make(map[string]struct{})
	}
	tc.m.RLock()
	defer tc.m.RUnlock()

	result := make(map[string]struct{})
	for id := range tc.pendingToolIDs {
		result[id] = struct{}{}
	}
	return result
}

// ProcessLogFileForCompletions convenience function to process a specific log file
func ProcessLogFileForCompletions(filepath string, pendingToolIDs map[string]struct{}, onCompletion func(ToolCompletionEvent)) error {
	if filepath == "" {
		return fmt.Errorf("empty filepath")
	}

	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	correlator := NewToolCompletionCorrelator(pendingToolIDs, onCompletion)
	return correlator.StreamAndDetectCompletions(file)
}
