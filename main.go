package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func isatty() bool {
	fd := os.Stdin.Fd()
	var termios syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cosmos <action> [args...]")
	fmt.Fprintln(os.Stderr, "actions:")
	fmt.Fprintln(os.Stderr, "  start <image> [args...]  - Start a new container")
	fmt.Fprintln(os.Stderr, "  stop <container_id>      - Stop a container")
	fmt.Fprintln(os.Stderr, "  restart <container_id>   - Restart a container")
	fmt.Fprintln(os.Stderr, "  status                   - Check proxy status")
	fmt.Fprintln(os.Stderr, "  stop-proxy              - Stop the cosmos proxy")
}

func spawnProxy() (string, error) {
	// Check if proxy container already exists
	checkCmd := exec.Command("docker", "ps", "-a", "--filter", "name=cosmos-proxy", "--format", "{{.ID}}")
	output, err := checkCmd.Output()
	if err == nil && len(strings.TrimSpace(string(output))) > 0 {
		containerID := strings.TrimSpace(string(output))
		// Check if it's running
		statusCmd := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", containerID)
		statusOutput, err := statusCmd.Output()
		if err == nil && strings.TrimSpace(string(statusOutput)) == "true" {
			fmt.Fprintf(os.Stderr, "Using existing proxy container: %s\n", containerID[:12])
			return containerID, nil
		}
		// Start existing container
		fmt.Fprintf(os.Stderr, "Starting existing proxy container: %s\n", containerID[:12])
		startCmd := exec.Command("docker", "start", containerID)
		if err := startCmd.Run(); err == nil {
			return containerID, nil
		}
		// If start fails, remove and create new
		exec.Command("docker", "rm", containerID).Run()
	}

	fmt.Fprintf(os.Stderr, "Spawning new coding-proxy container...\n")

	cmd := exec.Command("docker", "run", "-d",
		"--name", "cosmos-proxy",
		"--network", "cosmos-net", 
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-p", "8080:8080",
		"tiborvass/coding-proxy")

	output, err = cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to spawn proxy container: %w", err)
	}

	containerID := strings.TrimSpace(string(output))
	fmt.Fprintf(os.Stderr, "Proxy container started: %s\n", containerID[:12])

	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		resp, err := http.Get("http://localhost:8080/")
		if err == nil {
			resp.Body.Close()
			break
		}
		if i == 9 {
			return "", fmt.Errorf("proxy container not ready after 10 seconds")
		}
	}

	return containerID, nil
}

func sendCommand(command string) (string, error) {
	resp, err := http.Post("http://localhost:8080/container", "application/json", strings.NewReader(command))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func main() {
	if len(os.Args) <= 1 {
		usage()
		return
	}

	action := os.Args[1]
	
	switch action {
	case "start":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: cosmos start <image> [args...]\n")
			return
		}
		
		if err := exec.Command("docker", "network", "create", "cosmos-net").Run(); err != nil {
			// Network might already exist, check if we can connect to docker
			if checkErr := exec.Command("docker", "version").Run(); checkErr != nil {
				fmt.Fprintf(os.Stderr, "Docker not available: %v\n", checkErr)
				os.Exit(1)
			}
		}
		
		// Try to use existing proxy, spawn new one if needed
		_, err := http.Get("http://localhost:8080/")
		if err != nil {
			_, err := spawnProxy()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error spawning proxy: %v\n", err)
				os.Exit(1)
			}
		}

		image := os.Args[2]
		args := os.Args[3:]
		
		cmdJSON := map[string]any{
			"action": "start",
			"image":  image,
			"args":   args,
		}
		cmdBytes, _ := json.Marshal(cmdJSON)
		
		containerID, err := sendCommand(string(cmdBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error starting container: %v\n", err)
			os.Exit(1)
		}
		
		fmt.Fprintf(os.Stderr, "Container started: %s\n", containerID[:12])
		
		// Check if we have a TTY available and exec into container
		var execArgs []string
		if isatty() {
			execArgs = []string{"exec", "-ti", strings.TrimSpace(containerID), "/bin/bash"}
		} else {
			execArgs = []string{"exec", "-i", strings.TrimSpace(containerID), "/bin/bash"}
		}
		
		execCmd := exec.Command("docker", execArgs...)
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		if err := execCmd.Run(); err != nil {
			// Try with /bin/sh if bash fails
			if isatty() {
				execArgs = []string{"exec", "-ti", strings.TrimSpace(containerID), "/bin/sh"}
			} else {
				execArgs = []string{"exec", "-i", strings.TrimSpace(containerID), "/bin/sh"}
			}
			execCmd = exec.Command("docker", execArgs...)
			execCmd.Stdin = os.Stdin
			execCmd.Stdout = os.Stdout
			execCmd.Stderr = os.Stderr
			if err := execCmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to exec into container: %v\n", err)
			}
		}

	case "stop":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: cosmos stop <container_id>\n")
			return
		}
		
		containerID := os.Args[2]
		cmdJSON := map[string]any{
			"action": "stop",
			"container_id": containerID,
		}
		cmdBytes, _ := json.Marshal(cmdJSON)
		
		_, err := sendCommand(string(cmdBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping container: %v\n", err)
			os.Exit(1)
		}
		
		fmt.Fprintf(os.Stderr, "Container stopped: %s\n", containerID)

	case "restart":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: cosmos restart <container_id>\n")
			return
		}
		
		containerID := os.Args[2]
		cmdJSON := map[string]any{
			"action": "restart",
			"container_id": containerID,
		}
		cmdBytes, _ := json.Marshal(cmdJSON)
		
		_, err := sendCommand(string(cmdBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error restarting container: %v\n", err)
			os.Exit(1)
		}
		
		fmt.Fprintf(os.Stderr, "Container restarted: %s\n", containerID)

	case "status":
		resp, err := http.Get("http://localhost:8080/")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Proxy not running\n")
			os.Exit(1)
		}
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "Proxy running on :8080\n")

	case "stop-proxy":
		cmd := exec.Command("docker", "stop", "cosmos-proxy")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping proxy: %v\n", err)
			os.Exit(1)
		}
		cmd = exec.Command("docker", "rm", "cosmos-proxy")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing proxy: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Proxy stopped and removed\n")

	default:
		usage()
	}
}
