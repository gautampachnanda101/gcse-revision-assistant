package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/argon2"
)

const (
	cloudAIKeyService = appName
	cloudAIKeyUser    = "cloud-ai-api-key"
	cloudGateUser     = "cloud-ai-grown-up-password"
	maxCloudBody      = 1 << 20
	cloudSessionIdle  = 15 * time.Minute
)

type credentialStore interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type cloudAIConfig struct {
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}

type cloudAIStatus struct {
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	Configured bool   `json:"configured"`
}

type cloudAIManager struct {
	mu         sync.Mutex
	configPath string
	secrets    credentialStore
	client     *http.Client
	auth       *cloudAuthManager
}

type cloudAuthManager struct {
	mu          sync.Mutex
	secrets     credentialStore
	sessions    map[string]time.Time
	failures    int
	lockedUntil time.Time
}

func newCloudAIManager() (*cloudAIManager, error) {
	dir, err := userConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find user config directory: %w", err)
	}
	return newCloudAIManagerAt(filepath.Join(dir, appName, "cloud-ai.json"), keyringStore{}), nil
}

func newCloudAIManagerAt(configPath string, secrets credentialStore) *cloudAIManager {
	return &cloudAIManager{
		configPath: configPath,
		secrets:    secrets,
		auth:       &cloudAuthManager{secrets: secrets, sessions: map[string]time.Time{}},
		client: &http.Client{
			Timeout: 90 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (a *cloudAuthManager) configured() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := a.secrets.Get(cloudAIKeyService, cloudGateUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read grown-up password from the system credential store: %w", err)
	}
	return true, nil
}

func (a *cloudAuthManager) login(password, confirmation string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Before(a.lockedUntil) {
		return "", fmt.Errorf("too many attempts; try again in %s", time.Until(a.lockedUntil).Round(time.Second))
	}
	encoded, err := a.secrets.Get(cloudAIKeyService, cloudGateUser)
	if errors.Is(err, keyring.ErrNotFound) {
		encoded = ""
	} else if err != nil {
		return "", fmt.Errorf("read grown-up password from the system credential store: %w", err)
	}
	if encoded == "" {
		if len([]rune(password)) < 10 || len(password) > 1024 {
			return "", errors.New("choose a grown-up password with at least 10 characters")
		}
		if password != confirmation {
			return "", errors.New("the passwords do not match")
		}
		encoded, err = hashCloudPassword(password)
		if err != nil {
			return "", err
		}
		if err := a.secrets.Set(cloudAIKeyService, cloudGateUser, encoded); err != nil {
			return "", fmt.Errorf("save grown-up password in the system credential store: %w", err)
		}
	} else if !verifyCloudPassword(encoded, password) {
		a.failures++
		if a.failures >= 5 {
			a.failures = 0
			a.lockedUntil = now.Add(time.Minute)
		}
		return "", errors.New("password incorrect")
	}
	a.failures, a.lockedUntil = 0, time.Time{}
	token, err := newLocalAuthToken()
	if err != nil {
		return "", err
	}
	a.sessions[token] = now
	return token, nil
}

func hashCloudPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(randReader, salt); err != nil {
		return "", fmt.Errorf("create password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	return strings.Join([]string{
		"argon2id", "2", "65536", "1",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	}, "$"), nil
}

func verifyCloudPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	iterations, err1 := strconv.ParseUint(parts[1], 10, 32)
	memory, err2 := strconv.ParseUint(parts[2], 10, 32)
	threads, err3 := strconv.ParseUint(parts[3], 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil ||
		iterations < 1 || iterations > 10 || memory < 8*1024 || memory > 256*1024 || threads < 1 || threads > 8 || len(salt) < 16 || len(salt) > 64 || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (a *cloudAuthManager) authorize(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	lastUsed, ok := a.sessions[token]
	if !ok {
		return false
	}
	if time.Since(lastUsed) > cloudSessionIdle {
		delete(a.sessions, token)
		return false
	}
	a.sessions[token] = time.Now()
	return true
}

func (a *cloudAuthManager) revoke(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

func decodeCloudJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after JSON value")
	}
	return nil
}

type keyringStore struct{}

func (keyringStore) Get(service, user string) (string, error) {
	return keyringGet(service, user)
}

func (keyringStore) Set(service, user, password string) error {
	return keyringSet(service, user, password)
}

func (keyringStore) Delete(service, user string) error {
	return keyringDelete(service, user)
}

func validateCloudEndpoint(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("enter a valid provider URL without a username, query, or fragment")
	}
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname())) {
		return "", errors.New("provider URLs must use HTTPS (HTTP is allowed only for localhost)")
	}
	return raw, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newLocalAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("create local session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func hasLocalAuth(r *http.Request, token string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return len(got) == len(token) && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func (m *cloudAIManager) readConfig() (cloudAIConfig, error) {
	var cfg cloudAIConfig
	b, err := os.ReadFile(m.configPath)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read cloud AI settings: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("read cloud AI settings: %w", err)
	}
	return cfg, nil
}

func (m *cloudAIManager) status() (cloudAIStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.readConfig()
	if err != nil {
		return cloudAIStatus{}, err
	}
	key, err := m.secrets.Get(cloudAIKeyService, cloudAIKeyUser)
	if errors.Is(err, keyring.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return cloudAIStatus{}, fmt.Errorf("read cloud AI key from the system credential store: %w", err)
	}
	return cloudAIStatus{Endpoint: cfg.Endpoint, Model: cfg.Model, Configured: key != "" && cfg.Endpoint != "" && cfg.Model != ""}, nil
}

func (m *cloudAIManager) save(cfg cloudAIConfig, apiKey string) error {
	endpoint, err := validateCloudEndpoint(cfg.Endpoint)
	if err != nil {
		return err
	}
	cfg.Endpoint = endpoint
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" || len(cfg.Model) > 256 || strings.ContainsAny(cfg.Model, "\r\n") {
		return errors.New("enter a valid model name")
	}
	apiKey = strings.TrimSpace(apiKey)

	m.mu.Lock()
	defer m.mu.Unlock()
	previousKey, previousErr := m.secrets.Get(cloudAIKeyService, cloudAIKeyUser)
	if errors.Is(previousErr, keyring.ErrNotFound) {
		previousErr = nil
	}
	if previousErr != nil {
		return fmt.Errorf("read cloud AI key from the system credential store: %w", previousErr)
	}
	if apiKey == "" && previousKey == "" {
		return errors.New("enter an API key")
	}
	if apiKey != "" {
		if err := m.secrets.Set(cloudAIKeyService, cloudAIKeyUser, apiKey); err != nil {
			return fmt.Errorf("save cloud AI key to the system credential store: %w", err)
		}
	}
	b, err := marshalCloudJSON(cfg)
	if err == nil {
		err = writeFileAtomic(m.configPath, append(b, '\n'))
	}
	if err != nil {
		if apiKey != "" {
			var rollbackErr error
			if previousKey == "" {
				rollbackErr = m.secrets.Delete(cloudAIKeyService, cloudAIKeyUser)
				if errors.Is(rollbackErr, keyring.ErrNotFound) {
					rollbackErr = nil
				}
			} else {
				rollbackErr = m.secrets.Set(cloudAIKeyService, cloudAIKeyUser, previousKey)
			}
			if rollbackErr != nil {
				return fmt.Errorf("save cloud AI settings: %w (also failed to restore the previous credential: %v)", err, rollbackErr)
			}
		}
		return fmt.Errorf("save cloud AI settings: %w", err)
	}
	return nil
}

func (m *cloudAIManager) clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.secrets.Delete(cloudAIKeyService, cloudAIKeyUser); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("remove cloud AI key from the system credential store: %w", err)
	}
	if err := os.Remove(m.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove cloud AI settings: %w", err)
	}
	return nil
}

type cloudChatRequest struct {
	Messages []chatMessage `json:"messages"`
}

func (m *cloudAIManager) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var input cloudChatRequest
	if err := decodeCloudJSON(w, r, maxCloudBody, &input); err != nil {
		http.Error(w, "invalid chat request", http.StatusBadRequest)
		return
	}
	if len(input.Messages) == 0 || len(input.Messages) > 50 {
		http.Error(w, "chat request must contain 1 to 50 messages", http.StatusBadRequest)
		return
	}
	for _, msg := range input.Messages {
		if msg.Role != "system" && msg.Role != "user" && msg.Role != "assistant" {
			http.Error(w, "invalid chat message role", http.StatusBadRequest)
			return
		}
	}

	m.mu.Lock()
	cfg, err := m.readConfig()
	var apiKey string
	if err == nil {
		apiKey, err = m.secrets.Get(cloudAIKeyService, cloudAIKeyUser)
	}
	m.mu.Unlock()
	if errors.Is(err, keyring.ErrNotFound) {
		err = errors.New("no cloud AI key is configured")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	endpoint, err := validateCloudEndpoint(cfg.Endpoint)
	if err != nil || cfg.Model == "" {
		http.Error(w, "cloud AI is not configured", http.StatusServiceUnavailable)
		return
	}
	payload, err := marshalCloudJSON(struct {
		Model       string        `json:"model"`
		Messages    []chatMessage `json:"messages"`
		Temperature float64       `json:"temperature"`
		MaxTokens   int           `json:"max_tokens"`
	}{cfg.Model, input.Messages, 0.2, 650})
	if err != nil {
		http.Error(w, "could not encode chat request", http.StatusInternalServerError)
		return
	}
	req, err := newCloudRequest(r.Context(), http.MethodPost, endpoint+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, "invalid provider URL", http.StatusBadRequest)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	res, err := m.client.Do(req)
	if err != nil {
		http.Error(w, "cloud provider request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxCloudBody+1))
	if err != nil {
		http.Error(w, "could not read cloud provider response", http.StatusBadGateway)
		return
	}
	if len(body) > maxCloudBody {
		http.Error(w, "cloud provider response is too large", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(body)
}

func writeCloudAIError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
