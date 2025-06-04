package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Example showing how to integrate the tool correlator with the existing proxy
func ExampleIntegrateWithProxy() {
	// Get the tool IDs currently tracked by the proxy (from proxy.go:32)
	// In real usage, this would be passed from the proxy module
	proxyTrackedToolIDs := map[string]struct{}{
		"toolu_01SsyiDXrX6JQ9XHU285Jgo3": {},
		"toolu_01ULaG6tFQrhjDFBB9JwkXAL": {},
		// Add more tool IDs from your proxy's toolUseIDs map
	}
	
	// Set up completion handler
	onToolCompletion := func(event ToolCompletionEvent) {
		fmt.Printf("Tool %s completed at %v (line %d)\n", 
			event.ToolID, event.Timestamp, event.LineNo)
		
		// You can now:
		// 1. Remove from proxy's toolUseIDs map
		// 2. Log completion
		// 3. Trigger any cleanup or post-processing
		// 4. Update metrics/monitoring
	}
	
	// Create correlator
	correlator := NewToolCompletionCorrelator(proxyTrackedToolIDs, onToolCompletion)
	
	// Find a recent log file from Claude
	logDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", "-Users-home-cosmos")
	files, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Printf("Error reading log directory: %v\n", err)
		return
	}
	
	if len(files) == 0 {
		fmt.Println("No log files found")
		return
	}
	
	// Process the most recent log file
	logFile := filepath.Join(logDir, files[len(files)-1].Name())
	fmt.Printf("Processing log file: %s\n", logFile)
	
	// Process the log file for completions
	err = ProcessLogFileForCompletions(logFile, proxyTrackedToolIDs, onToolCompletion)
	if err != nil {
		fmt.Printf("Error processing log file: %v\n", err)
		return
	}
	
	// Show remaining pending tools
	remaining := correlator.GetPendingTools()
	fmt.Printf("Still pending: %d tools\n", len(remaining))
	for toolID := range remaining {
		fmt.Printf("  - %s\n", toolID)
	}
}

// Integration point for the proxy - call this when SSE detects new tool_use
func OnToolUseDetected(toolID string, correlator *ToolCompletionCorrelator) {
	correlator.AddPendingTool(toolID)
	fmt.Printf("Started tracking tool: %s\n", toolID)
}

// Helper to stream from an active log file (e.g., tail -f style)
func StreamActiveLogFile(filepath string, correlator *ToolCompletionCorrelator) {
	// This is where you'd implement real-time streaming
	// Could use fsnotify or similar to watch for new lines
	// For now, just process the existing file
	file, err := os.Open(filepath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}
	defer file.Close()
	
	correlator.StreamAndDetectCompletions(file)
}