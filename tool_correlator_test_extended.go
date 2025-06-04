package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestExtendedToolCorrelator(t *testing.T) {
	t.Run("NilSafety", func(t *testing.T) {
		// Test nil correlator
		var nilCorrelator *ToolCompletionCorrelator
		pending := nilCorrelator.GetPendingTools()
		if len(pending) != 0 {
			t.Errorf("Expected empty map from nil correlator, got %d items", len(pending))
		}

		// Test nil operations
		nilCorrelator.AddPendingTool("test")
		nilCorrelator.markToolCompleted("test")
		if nilCorrelator.isPendingTool("test") {
			t.Error("Nil correlator should return false for isPendingTool")
		}

		// Test empty string operations
		correlator := NewToolCompletionCorrelator(nil, nil)
		correlator.AddPendingTool("")
		pending = correlator.GetPendingTools()
		if len(pending) != 0 {
			t.Errorf("Empty tool ID should not be added, got %d items", len(pending))
		}

		// Test nil reader
		err := correlator.StreamAndDetectCompletions(nil)
		if err == nil {
			t.Error("Expected error for nil reader")
		}
	})

	t.Run("MalformedJSON", func(t *testing.T) {
		pendingTools := map[string]struct{}{
			"toolu_test": {},
		}

		var completedTools []ToolCompletionEvent
		correlator := NewToolCompletionCorrelator(pendingTools, func(event ToolCompletionEvent) {
			completedTools = append(completedTools, event)
		})

		// Capture stderr to verify error logging
		old := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		// Mix of valid and invalid JSON
		jsonlStream := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_test","type":"tool_result","content":"Result"}]},"timestamp":"2025-06-03T21:48:07.681Z"}
{invalid json here}
{"type":"user","message":{"role":"user","content":"Normal message"},"timestamp":"2025-06-03T21:48:08.681Z"}`

		reader := strings.NewReader(jsonlStream)
		err := correlator.StreamAndDetectCompletions(reader)

		w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		io.Copy(&buf, r)
		stderrOutput := buf.String()

		// Should complete with error but still process valid lines
		if err == nil {
			t.Error("Expected error for malformed JSON")
		}

		// Should have logged the error
		if !strings.Contains(stderrOutput, "WARNING: Failed to decode JSON") {
			t.Error("Expected warning message in stderr")
		}

		// Should have processed the first valid line
		if len(completedTools) != 1 {
			t.Errorf("Expected 1 completion from valid line, got %d", len(completedTools))
		}
	})

	t.Run("PanicInCallback", func(t *testing.T) {
		pendingTools := map[string]struct{}{
			"toolu_panic": {},
		}

		// Capture stderr to verify panic recovery message
		old := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		// Callback that panics
		panicCallback := func(event ToolCompletionEvent) {
			panic("test panic")
		}

		correlator := NewToolCompletionCorrelator(pendingTools, panicCallback)

		jsonlStream := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_panic","type":"tool_result","content":"Result"}]},"timestamp":"2025-06-03T21:48:07.681Z"}`

		reader := strings.NewReader(jsonlStream)

		// Should not panic the entire program
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Panic escaped callback: %v", r)
			}
		}()

		err := correlator.StreamAndDetectCompletions(reader)

		w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		io.Copy(&buf, r)
		stderrOutput := buf.String()

		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}

		// Should have logged the panic
		if !strings.Contains(stderrOutput, "ERROR: Panic in completion callback") {
			t.Error("Expected panic recovery message in stderr")
		}

		// Tool should still be marked as completed despite panic
		pending := correlator.GetPendingTools()
		if len(pending) != 0 {
			t.Errorf("Expected tool to be completed despite panic, got %d pending", len(pending))
		}
	})

	t.Run("ComplexNestedContent", func(t *testing.T) {
		pendingTools := map[string]struct{}{
			"toolu_nested1": {},
			"toolu_nested2": {},
		}

		var completedTools []ToolCompletionEvent
		correlator := NewToolCompletionCorrelator(pendingTools, func(event ToolCompletionEvent) {
			completedTools = append(completedTools, event)
		})

		// Multiple tool results in one message
		jsonlStream := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_nested1","type":"tool_result","content":"Result 1"},{"tool_use_id":"toolu_nested2","type":"tool_result","content":"Result 2"},{"type":"text","text":"Some text"}]},"timestamp":"2025-06-03T21:48:07.681Z"}`

		reader := strings.NewReader(jsonlStream)
		err := correlator.StreamAndDetectCompletions(reader)
		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}

		// Should have detected both completions
		if len(completedTools) != 2 {
			t.Fatalf("Expected 2 completions, got %d", len(completedTools))
		}

		// Check both tools were found
		foundTools := make(map[string]bool)
		for _, event := range completedTools {
			foundTools[event.ToolID] = true
		}

		if !foundTools["toolu_nested1"] || !foundTools["toolu_nested2"] {
			t.Error("Not all tools were detected in nested content")
		}
	})

	t.Run("EdgeCaseContent", func(t *testing.T) {
		pendingTools := map[string]struct{}{
			"toolu_edge": {},
		}

		var completedTools []ToolCompletionEvent
		correlator := NewToolCompletionCorrelator(pendingTools, func(event ToolCompletionEvent) {
			completedTools = append(completedTools, event)
		})

		// Test various edge cases
		jsonlStream := `{"type":"user","message":{"role":"user","content":[null,{"tool_use_id":"toolu_edge","type":"tool_result","content":"Result"},{"type":"tool_result"},{"tool_use_id":123,"type":"tool_result"}]},"timestamp":"2025-06-03T21:48:07.681Z"}`

		reader := strings.NewReader(jsonlStream)
		err := correlator.StreamAndDetectCompletions(reader)
		if err != nil {
			t.Fatalf("StreamAndDetectCompletions failed: %v", err)
		}

		// Should have detected only the valid completion
		if len(completedTools) != 1 {
			t.Errorf("Expected 1 completion, got %d", len(completedTools))
		}

		if len(completedTools) > 0 && completedTools[0].ToolID != "toolu_edge" {
			t.Errorf("Expected tool ID toolu_edge, got %s", completedTools[0].ToolID)
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		// Test concurrent access to the correlator
		correlator := NewToolCompletionCorrelator(map[string]struct{}{}, nil)

		done := make(chan bool)

		// Concurrent adds
		go func() {
			for i := 0; i < 100; i++ {
				correlator.AddPendingTool(string(rune(i)))
			}
			done <- true
		}()

		// Concurrent reads
		go func() {
			for i := 0; i < 100; i++ {
				correlator.GetPendingTools()
			}
			done <- true
		}()

		// Concurrent checks
		go func() {
			for i := 0; i < 100; i++ {
				correlator.isPendingTool(string(rune(i)))
			}
			done <- true
		}()

		// Concurrent removes
		go func() {
			for i := 0; i < 100; i++ {
				correlator.markToolCompleted(string(rune(i)))
			}
			done <- true
		}()

		// Wait for all goroutines
		for i := 0; i < 4; i++ {
			<-done
		}

		// Should not have panicked
	})
}
