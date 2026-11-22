package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed native/apple-fm-bridge
var appleFMBridge []byte

type appleChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type appleChatRequest struct {
	Messages []appleChatMessage `json:"messages"`
}

type appleChatResponse struct {
	Model   string `json:"model"`
	Content string `json:"content"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func appleBridgePath() string {
	if hostOS != "darwin" || hostArch != "arm64" || len(appleFMBridge) == 0 {
		return ""
	}
	path := filepath.Join(dataDir(), "native", appName+"-apple-ai")
	return path
}

func ensureAppleBridge() (string, error) {
	path := appleBridgePath()
	if path == "" {
		return "", errors.New("built-in Apple Intelligence is available only on Apple Silicon Macs")
	}
	if info, err := os.Stat(path); err == nil && info.Mode()&0o111 != 0 && info.Size() == int64(len(appleFMBridge)) {
		return path, nil
	}
	if err := writeFileAtomic(path, appleFMBridge); err != nil {
		return "", err
	}
	return path, chmod(path, 0o700)
}

func appleAIAvailable() (bool, string) {
	path, err := ensureAppleBridge()
	if err != nil {
		return false, err.Error()
	}
	out, err := execCommand(path, "--availability").CombinedOutput()
	if err == nil {
		return true, "Apple Intelligence"
	}
	return false, string(bytes.TrimSpace(out))
}

func chatWithAppleAI(messages []chatMessage) (string, error) {
	path, err := ensureAppleBridge()
	if err != nil {
		return "", err
	}
	req := appleChatRequest{Messages: make([]appleChatMessage, len(messages))}
	for i, m := range messages {
		req.Messages[i] = appleChatMessage{Role: m.Role, Content: m.Content}
	}
	body, _ := json.Marshal(req) // a struct of strings always marshals
	cmd := execCommand(path)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(stderr.Bytes(), &payload)
		if payload.Error != "" {
			return "", errors.New(payload.Error)
		}
		return "", fmt.Errorf("Apple Intelligence bridge: %w", err)
	}
	var res appleChatResponse
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return "", fmt.Errorf("invalid Apple Intelligence response: %w", err)
	}
	return res.Content, nil
}
