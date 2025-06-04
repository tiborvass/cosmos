package main

import (
	"strings"
	"testing"
	"time"
)

func TestToolCompletionCorrelator(t *testing.T) {
	t.Run("DetectsToolCompletion", func(t *testing.T) {
		// Simulate tool IDs that we know are pending from the proxy
		pendingTools := map[string]struct{}{
			"toolu_01SsyiDXrX6JQ9XHU285Jgo3": {},
			"toolu_01ULaG6tFQrhjDFBB9JwkXAL": {},
		}
		
		var completedTools []ToolCompletionEvent
		onCompletion := func(event ToolCompletionEvent) {
			completedTools = append(completedTools, event)
		}
		
		correlator := NewToolCompletionCorrelator(pendingTools, onCompletion)
		
		// Simulated JSONL stream with tool completion
		jsonlStream := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01SsyiDXrX6JQ9XHU285Jgo3","type":"tool_result","content":"File content here"}]},"timestamp":"2025-06-03T21:48:07.681Z"}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01ULaG6tFQrhjDFBB9JwkXAL","type":"tool_result","content":"Another file content"}]},"timestamp":"2025-06-03T21:48:08.681Z"}`
		
		reader := strings.NewReader(jsonlStream)
		err := correlator.StreamAndDetectCompletions(reader)
		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}
		
		// Should have detected 2 completions
		if len(completedTools) != 2 {
			t.Fatalf("Expected 2 completions, got %d", len(completedTools))
		}
		
		// Check first completion
		if completedTools[0].ToolID != "toolu_01SsyiDXrX6JQ9XHU285Jgo3" {
			t.Errorf("Expected tool ID toolu_01SsyiDXrX6JQ9XHU285Jgo3, got %s", completedTools[0].ToolID)
		}
		
		// Check second completion
		if completedTools[1].ToolID != "toolu_01ULaG6tFQrhjDFBB9JwkXAL" {
			t.Errorf("Expected tool ID toolu_01ULaG6tFQrhjDFBB9JwkXAL, got %s", completedTools[1].ToolID)
		}
		
		// Pending tools should now be empty
		remaining := correlator.GetPendingTools()
		if len(remaining) != 0 {
			t.Errorf("Expected no pending tools, got %d", len(remaining))
		}
	})
	
	t.Run("IgnoresUnknownToolCompletions", func(t *testing.T) {
		pendingTools := map[string]struct{}{
			"toolu_known": {},
		}
		
		var completedTools []ToolCompletionEvent
		onCompletion := func(event ToolCompletionEvent) {
			completedTools = append(completedTools, event)
		}
		
		correlator := NewToolCompletionCorrelator(pendingTools, onCompletion)
		
		// Stream with unknown tool completion
		jsonlStream := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_unknown","type":"tool_result","content":"Result"}]},"timestamp":"2025-06-03T21:48:07.681Z"}`
		
		reader := strings.NewReader(jsonlStream)
		err := correlator.StreamAndDetectCompletions(reader)
		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}
		
		// Should not have detected any completions
		if len(completedTools) != 0 {
			t.Fatalf("Expected 0 completions, got %d", len(completedTools))
		}
		
		// Known tool should still be pending
		remaining := correlator.GetPendingTools()
		if len(remaining) != 1 {
			t.Errorf("Expected 1 pending tool, got %d", len(remaining))
		}
	})
	
	t.Run("HandlesNonToolResultEntries", func(t *testing.T) {
		pendingTools := map[string]struct{}{
			"toolu_test": {},
		}
		
		var completedTools []ToolCompletionEvent
		correlator := NewToolCompletionCorrelator(pendingTools, func(event ToolCompletionEvent) {
			completedTools = append(completedTools, event)
		})
		
		// Stream with various non-tool-result entries
		jsonlStream := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Some response"}]},"timestamp":"2025-06-03T21:48:07.681Z"}
{"type":"user","message":{"role":"user","content":"Regular user message"},"timestamp":"2025-06-03T21:48:08.681Z"}
{"type":"summary","summary":"Session summary","timestamp":"2025-06-03T21:48:09.681Z"}`
		
		reader := strings.NewReader(jsonlStream)
		err := correlator.StreamAndDetectCompletions(reader)
		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}
		
		// Should not have detected any completions
		if len(completedTools) != 0 {
			t.Fatalf("Expected 0 completions, got %d", len(completedTools))
		}
	})
	
	t.Run("StreamingPerformance", func(t *testing.T) {
		// Test with larger dataset to ensure streaming is efficient
		pendingTools := map[string]struct{}{
			"toolu_perf_test": {},
		}
		
		var completionCount int
		correlator := NewToolCompletionCorrelator(pendingTools, func(event ToolCompletionEvent) {
			completionCount++
		})
		
		// Generate large JSONL stream
		var jsonlLines []string
		for i := 0; i < 1000; i++ {
			jsonlLines = append(jsonlLines, `{"type":"user","message":{"role":"user","content":"Normal entry"},"timestamp":"2025-06-03T21:48:07.681Z"}`)
		}
		// Add one completion at the end
		jsonlLines = append(jsonlLines, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_perf_test","type":"tool_result","content":"Result"}]},"timestamp":"2025-06-03T21:48:07.681Z"}`)
		
		jsonlStream := strings.Join(jsonlLines, "\n")
		reader := strings.NewReader(jsonlStream)
		
		start := time.Now()
		err := correlator.StreamAndDetectCompletions(reader)
		duration := time.Since(start)
		
		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}
		
		if completionCount != 1 {
			t.Errorf("Expected 1 completion, got %d", completionCount)
		}
		
		// Should complete within reasonable time (streaming should be fast)
		if duration > time.Second {
			t.Errorf("Processing took too long: %v", duration)
		}
		
		t.Logf("Processed 1001 lines in %v", duration)
	})
	
	t.Run("AddAndRemoveTools", func(t *testing.T) {
		correlator := NewToolCompletionCorrelator(map[string]struct{}{}, nil)
		
		// Add a tool
		correlator.AddPendingTool("toolu_dynamic")
		pending := correlator.GetPendingTools()
		if len(pending) != 1 {
			t.Errorf("Expected 1 pending tool after add, got %d", len(pending))
		}
		
		// Mark as completed
		correlator.markToolCompleted("toolu_dynamic")
		pending = correlator.GetPendingTools()
		if len(pending) != 0 {
			t.Errorf("Expected 0 pending tools after completion, got %d", len(pending))
		}
	})
}