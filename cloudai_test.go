package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

var errCloudTest = errors.New("injected cloud failure")

type cloudTestStore struct {
	*memoryCredentialStore
	getErr, setErr, deleteErr error
	getCalls, setCalls        int
	failGetAt, failSetAt      int
}

func (s *cloudTestStore) Get(service, user string) (string, error) {
	s.getCalls++
	if s.getErr != nil && (s.failGetAt == 0 || s.failGetAt == s.getCalls) {
		return "", s.getErr
	}
	return s.memoryCredentialStore.Get(service, user)
}

func (s *cloudTestStore) Set(service, user, value string) error {
	s.setCalls++
	if s.setErr != nil && (s.failSetAt == 0 || s.failSetAt == s.setCalls) {
		return s.setErr
	}
	return s.memoryCredentialStore.Set(service, user, value)
}

func (s *cloudTestStore) Delete(service, user string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.memoryCredentialStore.Delete(service, user)
}

type cloudRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cloudRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type memoryCredentialStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{data: map[string]string{}}
}

func (s *memoryCredentialStore) Get(service, user string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.data[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (s *memoryCredentialStore) Set(service, user, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[service+"/"+user] = password
	return nil
}

func (s *memoryCredentialStore) Delete(service, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := service + "/" + user
	if _, ok := s.data[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(s.data, key)
	return nil
}

func TestValidateCloudEndpoint(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"https://api.example.test/v1", true},
		{"http://127.0.0.1:8080/v1", true},
		{"http://[::1]:8080/v1", true},
		{"http://api.example.test/v1", false},
		{"https://user:pass@example.test/v1", false},
		{"https://example.test/v1?key=value", false},
		{"https://example.test/v1#fragment", false},
		{"not a URL", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := validateCloudEndpoint(tc.value)
			if (err == nil) != tc.valid {
				t.Fatalf("validateCloudEndpoint(%q) error = %v, valid %v", tc.value, err, tc.valid)
			}
		})
	}
}

func TestCloudAIStoresKeyOutsideConfigAndRemovesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloud-ai.json")
	secrets := newMemoryCredentialStore()
	manager := newCloudAIManagerAt(path, secrets)
	if err := manager.save(cloudAIConfig{Endpoint: "https://api.example.test/v1", Model: " model-name "}, " secret-value "); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-value") {
		t.Fatal("cloud API key was written to the settings file")
	}
	var cfg cloudAIConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "model-name" || cfg.Endpoint != "https://api.example.test/v1" {
		t.Fatalf("saved config = %+v", cfg)
	}
	status, err := manager.status()
	if err != nil || !status.Configured {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if err := manager.clear(); err != nil {
		t.Fatal(err)
	}
	status, err = manager.status()
	if err != nil || status.Configured {
		t.Fatalf("status after clear = %+v, %v", status, err)
	}
}

func TestCloudAIProxyUsesSavedKeyAndConfiguredModel(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("provider request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Errorf("provider Authorization = %q", got)
		}
		var payload struct {
			Model    string        `json:"model"`
			Messages []chatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		if payload.Model != "configured-model" || len(payload.Messages) != 1 || payload.Messages[0].Content != "hello" {
			t.Errorf("provider payload = %+v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer provider.Close()

	secrets := newMemoryCredentialStore()
	manager := newCloudAIManagerAt(filepath.Join(t.TempDir(), "cloud-ai.json"), secrets)
	if err := manager.save(cloudAIConfig{Endpoint: provider.URL + "/v1", Model: "configured-model"}, "provider-secret"); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/cloud-ai/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	manager.chat(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Fatalf("chat response = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/cloud-ai/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}{}`))
	manager.chat(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("chat with trailing JSON = %d", w.Code)
	}
}

func TestCloudAIProxyDoesNotFollowProviderRedirects(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Errorf("provider Authorization = %q", got)
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer provider.Close()

	manager := newCloudAIManagerAt(filepath.Join(t.TempDir(), "cloud-ai.json"), newMemoryCredentialStore())
	if err := manager.save(cloudAIConfig{Endpoint: provider.URL, Model: "configured-model"}, "provider-secret"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/cloud-ai/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
	manager.chat(w, r)
	if w.Code != http.StatusFound || redirected {
		t.Fatalf("redirect response = %d, target reached %v", w.Code, redirected)
	}
}

func TestCloudAIConfigAndChatRequireLocalSession(t *testing.T) {
	isolateHome(t)
	manager := newCloudAIManagerAt(filepath.Join(t.TempDir(), "cloud-ai.json"), newMemoryCredentialStore())
	h := newHandler(testHost, newAIManager("", false), manager, testLocalToken)
	if got := do(t, h, http.MethodGet, "/api/cloud-ai/auth", nil, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated auth GET = %d", got)
	}
	localHeaders := map[string]string{"Authorization": "Bearer " + testLocalToken}
	if got := do(t, h, http.MethodGet, "/api/cloud-ai/auth", nil, localHeaders).Code; got != http.StatusOK {
		t.Fatalf("local auth status = %d", got)
	}
	login := `{"password":"parent-password-long","confirmation":"parent-password-long"}`
	response := do(t, h, http.MethodPost, "/api/cloud-ai/auth", strings.NewReader(login), localHeaders)
	if response.Code != http.StatusOK {
		t.Fatalf("password setup = %d %s", response.Code, response.Body.String())
	}
	var auth struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &auth); err != nil || auth.SessionToken == "" {
		t.Fatalf("authentication response = %s (%v)", response.Body.String(), err)
	}
	headers := map[string]string{
		"Authorization":      "Bearer " + testLocalToken,
		"X-Cloud-AI-Session": auth.SessionToken,
	}
	if got := do(t, h, http.MethodGet, "/api/cloud-ai/config", nil, headers).Code; got != http.StatusOK {
		t.Fatalf("authenticated config GET = %d", got)
	}
	badOrigin := map[string]string{"Authorization": "Bearer " + testLocalToken, "X-Cloud-AI-Session": auth.SessionToken, "Origin": "http://evil.example"}
	if got := do(t, h, http.MethodPost, "/api/cloud-ai/config", strings.NewReader(`{}`), badOrigin).Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin config POST = %d", got)
	}
}

func TestCloudAuthVerifiesPasswordAndExpiresIdleSessions(t *testing.T) {
	secrets := newMemoryCredentialStore()
	auth := &cloudAuthManager{secrets: secrets, sessions: map[string]time.Time{}}
	if _, err := auth.login("short", "short"); err == nil {
		t.Fatal("short setup password accepted")
	}
	if _, err := auth.login("a-secure-parent-passphrase", "different"); err == nil {
		t.Fatal("mismatched setup password accepted")
	}
	token, err := auth.login("a-secure-parent-passphrase", "a-secure-parent-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.authorize(token) {
		t.Fatal("new session was rejected")
	}
	auth.mu.Lock()
	auth.failures = 4
	auth.mu.Unlock()
	if _, err := auth.login("wrong-password", ""); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := auth.login("a-secure-parent-passphrase", ""); err == nil {
		t.Fatal("password accepted during the attempt lockout")
	}
	auth.mu.Lock()
	auth.lockedUntil = time.Now().Add(-time.Second)
	auth.mu.Unlock()
	newToken, err := auth.login("a-secure-parent-passphrase", "")
	if err != nil {
		t.Fatal(err)
	}
	auth.mu.Lock()
	auth.sessions[newToken] = time.Now().Add(-cloudSessionIdle - time.Second)
	auth.mu.Unlock()
	if auth.authorize(newToken) {
		t.Fatal("idle session was accepted")
	}
	restarted := &cloudAuthManager{secrets: secrets, sessions: map[string]time.Time{}}
	configured, err := restarted.configured()
	if err != nil || !configured {
		t.Fatalf("stored password configuration = %v, %v", configured, err)
	}
	if _, err := restarted.login("a-secure-parent-passphrase", ""); err != nil {
		t.Fatalf("stored password did not survive manager restart: %v", err)
	}
}

func TestCloudAIConfigDoesNotReturnKey(t *testing.T) {
	secrets := newMemoryCredentialStore()
	manager := newCloudAIManagerAt(filepath.Join(t.TempDir(), "cloud-ai.json"), secrets)
	if err := manager.save(cloudAIConfig{Endpoint: "https://api.example.test/v1", Model: "model"}, "secret-never-returned"); err != nil {
		t.Fatal(err)
	}
	status, err := manager.status()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-never-returned") {
		t.Fatalf("status exposed the API key: %s", b)
	}
	if err := manager.secrets.Delete(cloudAIKeyService, cloudAIKeyUser); err != nil {
		t.Fatal(err)
	}
	status, err = manager.status()
	if err != nil || status.Configured {
		t.Fatalf("missing key status = %+v, %v", status, err)
	}
}

func TestCloudAuthLogoutRevokesSession(t *testing.T) {
	manager := newCloudAIManagerAt(filepath.Join(t.TempDir(), "cloud-ai.json"), newMemoryCredentialStore())
	h := newHandler(testHost, newAIManager("", false), manager, testLocalToken)
	headers := map[string]string{"Authorization": "Bearer " + testLocalToken}
	response := do(t, h, http.MethodPost, "/api/cloud-ai/auth", strings.NewReader(`{"password":"parent-password-long","confirmation":"parent-password-long"}`), headers)
	var auth struct {
		SessionToken string `json:"sessionToken"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &auth) != nil || auth.SessionToken == "" {
		t.Fatalf("password setup = %d %s", response.Code, response.Body.String())
	}
	headers["X-Cloud-AI-Session"] = auth.SessionToken
	if got := do(t, h, http.MethodDelete, "/api/cloud-ai/auth", nil, headers).Code; got != http.StatusOK {
		t.Fatalf("logout = %d", got)
	}
	if got := do(t, h, http.MethodGet, "/api/cloud-ai/config", nil, headers).Code; got != http.StatusUnauthorized {
		t.Fatalf("revoked session config GET = %d", got)
	}
}

type unavailableCredentialStore struct{}

func (unavailableCredentialStore) Get(string, string) (string, error) {
	return "", errors.New("credential store unavailable")
}
func (unavailableCredentialStore) Set(string, string, string) error {
	return errors.New("credential store unavailable")
}
func (unavailableCredentialStore) Delete(string, string) error {
	return errors.New("credential store unavailable")
}

func TestCloudAIRejectsUnavailableCredentialStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloud-ai.json")
	manager := newCloudAIManagerAt(path, unavailableCredentialStore{})
	if _, err := manager.auth.configured(); err == nil {
		t.Fatal("password status succeeded without a credential store")
	}
	if _, err := manager.auth.login("parent-password-long", "parent-password-long"); err == nil {
		t.Fatal("password setup succeeded without a credential store")
	}
	if err := manager.save(cloudAIConfig{Endpoint: "https://api.example.test/v1", Model: "model"}, "fake-key"); err == nil {
		t.Fatal("cloud configuration succeeded without a credential store")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration file created after store failure: %v", err)
	}
	if err := manager.clear(); err == nil {
		t.Fatal("credential deletion succeeded without a credential store")
	}
}

func TestSystemCredentialStoreRoundTrip(t *testing.T) {
	if os.Getenv("GCSE_TEST_SYSTEM_KEYRING") != "1" {
		t.Skip("set GCSE_TEST_SYSTEM_KEYRING=1 to test a disposable OS credential")
	}
	token, err := newLocalAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	service := appName + "-integration-test-" + token
	store := keyringStore{}
	if err := store.Set(service, "disposable", "test-value-not-a-secret"); err != nil {
		t.Fatalf("create disposable credential: %v", err)
	}
	deleteAttempted := false
	t.Cleanup(func() {
		if !deleteAttempted {
			if err := store.Delete(service, "disposable"); err != nil {
				t.Errorf("remove disposable credential: %v", err)
			}
		}
	})
	value, err := store.Get(service, "disposable")
	if err != nil || value != "test-value-not-a-secret" {
		t.Fatalf("read disposable credential = %q, %v", value, err)
	}
	deleteAttempted = true
	if err := store.Delete(service, "disposable"); err != nil {
		t.Fatalf("remove disposable credential: %v", err)
	}
	if _, err := store.Get(service, "disposable"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("deleted credential lookup error = %v", err)
	}
}

type isolatedSystemCredentialStore struct {
	service string
}

func (s isolatedSystemCredentialStore) Get(_ string, user string) (string, error) {
	return keyringStore{}.Get(s.service, user)
}

func (s isolatedSystemCredentialStore) Set(_ string, user, value string) error {
	return keyringStore{}.Set(s.service, user, value)
}

func (s isolatedSystemCredentialStore) Delete(_ string, user string) error {
	return keyringStore{}.Delete(s.service, user)
}

func TestSystemCloudAIFlow(t *testing.T) {
	if os.Getenv("GCSE_TEST_SYSTEM_KEYRING") != "1" {
		t.Skip("set GCSE_TEST_SYSTEM_KEYRING=1 to test disposable OS credentials")
	}
	id, err := newLocalAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	store := isolatedSystemCredentialStore{service: appName + "-integration-test-" + id}
	t.Cleanup(func() {
		for _, user := range []string{cloudGateUser, cloudAIKeyUser} {
			if err := store.Delete(cloudAIKeyService, user); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				t.Errorf("remove disposable credential %s: %v", user, err)
			}
		}
	})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-api-key-not-a-secret" {
			t.Errorf("unexpected provider request: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer provider.Close()

	path := filepath.Join(t.TempDir(), "cloud-ai.json")
	manager := newCloudAIManagerAt(path, store)
	handler := newHandler(testHost, newAIManager("", false), manager, testLocalToken)
	headers := map[string]string{"Authorization": "Bearer " + testLocalToken}
	login := `{"password":"test-parent-password","confirmation":"test-parent-password"}`
	response := do(t, handler, http.MethodPost, "/api/cloud-ai/auth", strings.NewReader(login), headers)
	var auth struct {
		SessionToken string `json:"sessionToken"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &auth) != nil || auth.SessionToken == "" {
		t.Fatalf("password setup = %d %s", response.Code, response.Body.String())
	}
	headers["X-Cloud-AI-Session"] = auth.SessionToken
	settings, err := json.Marshal(map[string]string{"endpoint": provider.URL + "/v1", "model": "test-model", "apiKey": "test-api-key-not-a-secret"})
	if err != nil {
		t.Fatal(err)
	}
	response = do(t, handler, http.MethodPost, "/api/cloud-ai/config", strings.NewReader(string(settings)), headers)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "test-api-key-not-a-secret") {
		t.Fatalf("config save = %d %s", response.Code, response.Body.String())
	}

	restarted := newCloudAIManagerAt(path, store)
	handler = newHandler(testHost, newAIManager("", false), restarted, testLocalToken)
	response = do(t, handler, http.MethodPost, "/api/cloud-ai/auth", strings.NewReader(`{"password":"test-parent-password"}`), headers)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &auth) != nil || auth.SessionToken == "" {
		t.Fatalf("sign-in after restart = %d %s", response.Code, response.Body.String())
	}
	headers["X-Cloud-AI-Session"] = auth.SessionToken
	response = do(t, handler, http.MethodPost, "/api/cloud-ai/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`), headers)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"hello"`) {
		t.Fatalf("cloud chat = %d %s", response.Code, response.Body.String())
	}
	response = do(t, handler, http.MethodDelete, "/api/cloud-ai/config", nil, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("config deletion = %d %s", response.Code, response.Body.String())
	}
}

func TestCloudCredentialAdapter(t *testing.T) {
	memory := newMemoryCredentialStore()
	setVar(t, &keyringGet, memory.Get)
	setVar(t, &keyringSet, memory.Set)
	setVar(t, &keyringDelete, memory.Delete)
	store := keyringStore{}
	if err := store.Set("service", "user", "value"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get("service", "user"); err != nil || got != "value" {
		t.Fatalf("credential = %q, %v", got, err)
	}
	if err := store.Delete("service", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("service", "user"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("deleted credential: %v", err)
	}
}

func TestCloudAuthFailurePaths(t *testing.T) {
	t.Run("config directory", func(t *testing.T) {
		setVar(t, &userConfigDir, func() (string, error) { return "", errCloudTest })
		if _, err := newCloudAIManager(); !errors.Is(err, errCloudTest) {
			t.Fatalf("config directory error = %v", err)
		}
	})
	t.Run("random salt", func(t *testing.T) {
		setVar(t, &randReader, io.Reader(failingReader{}))
		auth := newCloudAIManagerAt("", newMemoryCredentialStore()).auth
		if _, err := auth.login("parent-password", "parent-password"); err == nil || !strings.Contains(err.Error(), "password salt") {
			t.Fatalf("salt error = %v", err)
		}
	})
	t.Run("store password", func(t *testing.T) {
		store := &cloudTestStore{memoryCredentialStore: newMemoryCredentialStore(), setErr: errCloudTest}
		auth := newCloudAIManagerAt("", store).auth
		if _, err := auth.login("parent-password", "parent-password"); !errors.Is(err, errCloudTest) {
			t.Fatalf("password save error = %v", err)
		}
	})
	t.Run("random session", func(t *testing.T) {
		store := newMemoryCredentialStore()
		encoded, err := hashCloudPassword("parent-password")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set(cloudAIKeyService, cloudGateUser, encoded); err != nil {
			t.Fatal(err)
		}
		setVar(t, &randReader, io.Reader(failingReader{}))
		auth := newCloudAIManagerAt("", store).auth
		if _, err := auth.login("parent-password", ""); err == nil || !strings.Contains(err.Error(), "session token") {
			t.Fatalf("session error = %v", err)
		}
	})
	for _, invalid := range []string{"bad", "argon2id$0$65536$1$bad$bad", "argon2id$2$999999999$1$bad$bad"} {
		if verifyCloudPassword(invalid, "parent-password") {
			t.Fatalf("invalid password verifier accepted: %q", invalid)
		}
	}
	if !isLoopbackHost("LOCALHOST") || isLoopbackHost("not-a-host") {
		t.Fatal("loopback hostname validation failed")
	}
}

func TestCloudConfigFailuresAndRollback(t *testing.T) {
	cfg := cloudAIConfig{Endpoint: "https://example.test/v1", Model: "model"}
	t.Run("invalid settings", func(t *testing.T) {
		m := newCloudAIManagerAt(filepath.Join(t.TempDir(), "config"), newMemoryCredentialStore())
		for _, invalid := range []cloudAIConfig{{Endpoint: "http://example.test", Model: "m"}, {Endpoint: cfg.Endpoint}, {Endpoint: cfg.Endpoint, Model: "bad\nname"}} {
			if err := m.save(invalid, "key"); err == nil {
				t.Fatalf("accepted config %+v", invalid)
			}
		}
		if err := m.save(cfg, ""); err == nil {
			t.Fatal("accepted missing key")
		}
	})
	t.Run("unreadable and malformed config", func(t *testing.T) {
		dir := t.TempDir()
		m := newCloudAIManagerAt(dir, newMemoryCredentialStore())
		if _, err := m.status(); err == nil {
			t.Fatal("directory was readable as config")
		}
		m.configPath = filepath.Join(dir, "bad.json")
		writeFile(t, m.configPath, "{")
		if _, err := m.status(); err == nil {
			t.Fatal("malformed config accepted")
		}
	})
	t.Run("status key failure", func(t *testing.T) {
		m := newCloudAIManagerAt(filepath.Join(t.TempDir(), "config"), unavailableCredentialStore{})
		if _, err := m.status(); err == nil {
			t.Fatal("status ignored credential error")
		}
	})
	t.Run("save key failure", func(t *testing.T) {
		store := &cloudTestStore{memoryCredentialStore: newMemoryCredentialStore(), setErr: errCloudTest}
		m := newCloudAIManagerAt(filepath.Join(t.TempDir(), "config"), store)
		if err := m.save(cfg, "key"); !errors.Is(err, errCloudTest) {
			t.Fatalf("save error = %v", err)
		}
	})
	for _, tc := range []struct {
		name        string
		previous    string
		apiKey      string
		deleteErr   error
		failSetAt   int
		wantRestore bool
	}{
		{"remove new key", "", "new", nil, 0, true},
		{"already removed", "", "new", keyring.ErrNotFound, 0, false},
		{"restore old key", "old", "new", nil, 0, true},
		{"restore failure", "old", "new", nil, 2, false},
		{"delete failure", "", "new", errCloudTest, 0, false},
		{"unchanged key", "old", "", nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cloudTestStore{memoryCredentialStore: newMemoryCredentialStore(), deleteErr: tc.deleteErr, setErr: errCloudTest, failSetAt: tc.failSetAt}
			if tc.failSetAt == 0 {
				store.setErr = nil
			}
			if tc.previous != "" {
				_ = store.memoryCredentialStore.Set(cloudAIKeyService, cloudAIKeyUser, tc.previous)
			}
			m := newCloudAIManagerAt(filepath.Join(t.TempDir(), "config"), store)
			setVar(t, &marshalCloudJSON, func(any) ([]byte, error) { return nil, errCloudTest })
			err := m.save(cfg, tc.apiKey)
			if !errors.Is(err, errCloudTest) {
				t.Fatalf("save error = %v", err)
			}
			if tc.wantRestore {
				got, _ := store.memoryCredentialStore.Get(cloudAIKeyService, cloudAIKeyUser)
				if got != tc.previous {
					t.Fatalf("restored key = %q, want %q", got, tc.previous)
				}
			}
			if (tc.name == "restore failure" || tc.name == "delete failure") && !strings.Contains(err.Error(), "failed to restore") {
				t.Fatalf("rollback failure not reported: %v", err)
			}
		})
	}
	t.Run("config write failure", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "parent"), "not a directory")
		m := newCloudAIManagerAt(filepath.Join(dir, "parent", "config"), newMemoryCredentialStore())
		if err := m.save(cfg, "key"); err == nil {
			t.Fatal("write failure ignored")
		}
	})
	t.Run("clear file failure", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "child"), "keep")
		m := newCloudAIManagerAt(dir, newMemoryCredentialStore())
		if err := m.clear(); err == nil {
			t.Fatal("nonempty directory removal succeeded")
		}
	})
}

func TestCloudChatFailurePaths(t *testing.T) {
	const valid = `{"messages":[{"role":"user","content":"hello"}]}`
	for _, tc := range []struct {
		name, method, body string
		setup              func(*testing.T, *cloudAIManager)
		want               int
	}{
		{"method", "GET", valid, nil, 405},
		{"bad json", "POST", "{", nil, 400},
		{"empty messages", "POST", `{"messages":[]}`, nil, 400},
		{"bad role", "POST", `{"messages":[{"role":"admin","content":"x"}]}`, nil, 400},
		{"missing key", "POST", valid, func(t *testing.T, m *cloudAIManager) { _ = m.secrets.Delete(cloudAIKeyService, cloudAIKeyUser) }, 503},
		{"store failure", "POST", valid, func(t *testing.T, m *cloudAIManager) { m.secrets = unavailableCredentialStore{} }, 503},
		{"invalid config", "POST", valid, func(t *testing.T, m *cloudAIManager) {
			writeFile(t, m.configPath, `{"endpoint":"http://example.test","model":"m"}`)
		}, 503},
		{"marshal failure", "POST", valid, func(t *testing.T, m *cloudAIManager) {
			setVar(t, &marshalCloudJSON, func(any) ([]byte, error) { return nil, errCloudTest })
		}, 500},
		{"request failure", "POST", valid, func(t *testing.T, m *cloudAIManager) {
			setVar(t, &newCloudRequest, func(context.Context, string, string, io.Reader) (*http.Request, error) { return nil, errCloudTest })
		}, 400},
		{"transport failure", "POST", valid, func(t *testing.T, m *cloudAIManager) {
			m.client.Transport = cloudRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errCloudTest })
		}, 502},
		{"response read failure", "POST", valid, func(t *testing.T, m *cloudAIManager) {
			m.client.Transport = cloudRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(failingReader{})}, nil
			})
		}, 502},
		{"response too large", "POST", valid, func(t *testing.T, m *cloudAIManager) {
			m.client.Transport = cloudRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxCloudBody+1)))}, nil
			})
		}, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryCredentialStore()
			m := newCloudAIManagerAt(filepath.Join(t.TempDir(), "config"), store)
			writeFile(t, m.configPath, `{"endpoint":"https://example.test/v1","model":"m"}`)
			_ = store.Set(cloudAIKeyService, cloudAIKeyUser, "key")
			if tc.setup != nil {
				tc.setup(t, m)
			}
			w := httptest.NewRecorder()
			m.chat(w, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))
			if w.Code != tc.want {
				t.Fatalf("response = %d %s, want %d", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}

func TestCloudRoutesFailuresAndMethods(t *testing.T) {
	const local = "Bearer " + testLocalToken
	for _, tc := range []struct {
		name, method, path, body string
		setup                    func(*cloudAIManager, *cloudTestStore)
		want                     int
	}{
		{"auth status error", "GET", "/api/cloud-ai/auth", "", func(m *cloudAIManager, s *cloudTestStore) { s.getErr = errCloudTest }, 503},
		{"auth bad json", "POST", "/api/cloud-ai/auth", "{", nil, 400},
		{"auth invalid password", "POST", "/api/cloud-ai/auth", `{"password":"short","confirmation":"short"}`, nil, 401},
		{"auth method", "PUT", "/api/cloud-ai/auth", "", nil, 405},
		{"config status error", "GET", "/api/cloud-ai/config", "", func(m *cloudAIManager, s *cloudTestStore) { s.getErr = errCloudTest }, 503},
		{"config bad json", "POST", "/api/cloud-ai/config", "{", nil, 400},
		{"config invalid", "POST", "/api/cloud-ai/config", `{}`, nil, 400},
		{"config saved", "POST", "/api/cloud-ai/config", `{"endpoint":"https://example.test","model":"m","apiKey":"key"}`, nil, 200},
		{"config status after save error", "POST", "/api/cloud-ai/config", `{"endpoint":"https://example.test","model":"m","apiKey":"key"}`, func(m *cloudAIManager, s *cloudTestStore) { s.getErr = errCloudTest; s.failGetAt = 2 }, 503},
		{"config delete failure", "DELETE", "/api/cloud-ai/config", "", func(m *cloudAIManager, s *cloudTestStore) { s.deleteErr = errCloudTest }, 500},
		{"config delete", "DELETE", "/api/cloud-ai/config", "", nil, 200},
		{"config method", "PUT", "/api/cloud-ai/config", "", nil, 405},
		{"chat unauthorized", "POST", "/api/cloud-ai/chat/completions", `{}`, func(m *cloudAIManager, s *cloudTestStore) { delete(m.auth.sessions, "session") }, 401},
		{"chat authorized", "POST", "/api/cloud-ai/chat/completions", `{}`, nil, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cloudTestStore{memoryCredentialStore: newMemoryCredentialStore()}
			m := newCloudAIManagerAt(filepath.Join(t.TempDir(), "config"), store)
			m.auth.sessions["session"] = time.Now()
			if tc.setup != nil {
				tc.setup(m, store)
			}
			h := newHandler(testHost, newAIManager("", false), m, testLocalToken)
			headers := map[string]string{"Authorization": local, "X-Cloud-AI-Session": "session"}
			response := do(t, h, tc.method, tc.path, strings.NewReader(tc.body), headers)
			if response.Code != tc.want {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), tc.want)
			}
		})
	}
}

func TestServeCloudInitializationFailuresCloseListener(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T)
	}{
		{"config directory", func(t *testing.T) {
			setVar(t, &userConfigDir, func() (string, error) { return "", errCloudTest })
		}},
		{"session token", func(t *testing.T) {
			setVar(t, &randReader, io.Reader(failingReader{}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			var addr string
			setVar(t, &listen, func(network, address string) (net.Listener, error) {
				ln, err := net.Listen(network, address)
				if err == nil {
					addr = ln.Addr().String()
				}
				return ln, err
			})
			tc.setup(t)
			if err := serveContext(context.Background(), options{port: 0, offline: true, noAI: true, noBrowser: true, noAutoUp: true}); err == nil {
				t.Fatal("cloud initialization failure ignored")
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatalf("listener not released: %v", err)
			}
			_ = ln.Close()
		})
	}
}
