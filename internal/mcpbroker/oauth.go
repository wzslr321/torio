package mcpbroker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"github.com/wzslr321/torio/internal/lima"
)

const (
	oauthSessionSchema = "1"
	maxOAuthStateBytes = 64 << 10
)

// ErrOAuthSessionNotFound is the daemon's actionable precondition failure: the
// operator must complete `torio mcp login <service>` before the socket exists.
var ErrOAuthSessionNotFound = errors.New("OAuth session not found")

// SessionStorage is the private OAuth state boundary used both by interactive
// login and daemon refresh. Implementations must not log or render stored
// values on error.
type SessionStorage interface {
	Save(service string, config *oauth2.Config, token *oauth2.Token) error
	Load(service string) (*oauth2.Config, *oauth2.Token, error)
}

type sessionStore struct{ dir string }

// NewSessionStore returns the crash-safe per-service store rooted at dir.
func NewSessionStore(dir string) *sessionStore { return &sessionStore{dir: dir} }

type oauthSessionDocument struct {
	SchemaVersion string           `json:"schema_version"`
	Config        oauthConfigState `json:"config"`
	Token         oauthTokenState  `json:"token"`
}

type oauthConfigState struct {
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret,omitempty"`
	AuthURL       string   `json:"auth_url"`
	TokenURL      string   `json:"token_url"`
	DeviceAuthURL string   `json:"device_auth_url,omitempty"`
	AuthStyle     int      `json:"auth_style,omitempty"`
	RedirectURL   string   `json:"redirect_url"`
	Scopes        []string `json:"scopes,omitempty"`
}

type oauthTokenState struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

func sessionDocument(config *oauth2.Config, token *oauth2.Token) (oauthSessionDocument, error) {
	if config == nil || token == nil {
		return oauthSessionDocument{}, errors.New("OAuth config and token are required")
	}
	if config.ClientID == "" || config.Endpoint.AuthURL == "" || config.Endpoint.TokenURL == "" || config.RedirectURL == "" {
		return oauthSessionDocument{}, errors.New("OAuth config is incomplete")
	}
	if token.AccessToken == "" {
		return oauthSessionDocument{}, errors.New("OAuth access token is empty")
	}
	return oauthSessionDocument{
		SchemaVersion: oauthSessionSchema,
		Config: oauthConfigState{
			ClientID:      config.ClientID,
			ClientSecret:  config.ClientSecret,
			AuthURL:       config.Endpoint.AuthURL,
			TokenURL:      config.Endpoint.TokenURL,
			DeviceAuthURL: config.Endpoint.DeviceAuthURL,
			AuthStyle:     int(config.Endpoint.AuthStyle),
			RedirectURL:   config.RedirectURL,
			Scopes:        append([]string(nil), config.Scopes...),
		},
		Token: oauthTokenState{
			AccessToken:  token.AccessToken,
			TokenType:    token.TokenType,
			RefreshToken: token.RefreshToken,
			Expiry:       token.Expiry.UTC(),
		},
	}, nil
}

func (d oauthSessionDocument) values() (*oauth2.Config, *oauth2.Token, error) {
	if d.SchemaVersion != oauthSessionSchema {
		return nil, nil, errors.New("unsupported OAuth session schema")
	}
	cfg := &oauth2.Config{
		ClientID:     d.Config.ClientID,
		ClientSecret: d.Config.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:       d.Config.AuthURL,
			TokenURL:      d.Config.TokenURL,
			DeviceAuthURL: d.Config.DeviceAuthURL,
			AuthStyle:     oauth2.AuthStyle(d.Config.AuthStyle),
		},
		RedirectURL: d.Config.RedirectURL,
		Scopes:      append([]string(nil), d.Config.Scopes...),
	}
	token := &oauth2.Token{
		AccessToken:  d.Token.AccessToken,
		TokenType:    d.Token.TokenType,
		RefreshToken: d.Token.RefreshToken,
		Expiry:       d.Token.Expiry,
	}
	if _, err := sessionDocument(cfg, token); err != nil {
		return nil, nil, err
	}
	return cfg, token, nil
}

func (s *sessionStore) Save(service string, config *oauth2.Config, token *oauth2.Token) error {
	if err := lima.ValidateServiceName(service); err != nil {
		return err
	}
	doc, err := sessionDocument(config, token)
	if err != nil {
		return err
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	path := s.path(service)
	if err := validatePrivateFileIfPresent(path); err != nil {
		return err
	}

	temp, err := os.CreateTemp(s.dir, ".session-*")
	if err != nil {
		return errors.New("create temporary OAuth session")
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return errors.New("set temporary OAuth session mode")
	}
	enc := json.NewEncoder(temp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return errors.New("encode OAuth session")
	}
	if err := temp.Sync(); err != nil {
		return errors.New("sync OAuth session")
	}
	if err := temp.Close(); err != nil {
		return errors.New("close OAuth session")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.New("commit OAuth session")
	}
	committed = true

	dir, err := os.Open(s.dir)
	if err != nil {
		return errors.New("open OAuth session directory for sync")
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return errors.New("sync OAuth session directory")
	}
	return nil
}

func (s *sessionStore) Load(service string) (*oauth2.Config, *oauth2.Token, error) {
	if err := lima.ValidateServiceName(service); err != nil {
		return nil, nil, err
	}
	if err := validatePrivateDir(s.dir); err != nil {
		return nil, nil, err
	}
	path := s.path(service)
	if err := validatePrivateFile(path); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("open OAuth session")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxOAuthStateBytes+1))
	if err != nil {
		return nil, nil, errors.New("read OAuth session")
	}
	if len(data) > maxOAuthStateBytes {
		return nil, nil, errors.New("OAuth session exceeds size limit")
	}
	var doc oauthSessionDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, errors.New("decode OAuth session")
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, nil, err
	}
	return doc.values()
}

func (s *sessionStore) path(service string) string {
	return filepath.Join(s.dir, service+".json")
}

func (s *sessionStore) ensureDir() error {
	info, err := os.Lstat(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(s.dir, 0o700); err != nil {
			return errors.New("create OAuth session directory")
		}
		return validatePrivateDir(s.dir)
	}
	if err != nil {
		return errors.New("inspect OAuth session directory")
	}
	return validatePrivateInfo("OAuth session directory", info, true, 0o700)
}

func validatePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrOAuthSessionNotFound
	}
	if err != nil {
		return errors.New("inspect OAuth session directory")
	}
	return validatePrivateInfo("OAuth session directory", info, true, 0o700)
}

func validatePrivateFileIfPresent(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("inspect OAuth session")
	}
	return validatePrivateFile(path)
}

func validatePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrOAuthSessionNotFound
	}
	if err != nil {
		return errors.New("inspect OAuth session")
	}
	return validatePrivateInfo("OAuth session", info, false, 0o600)
}

func validatePrivateInfo(subject string, info os.FileInfo, directory bool, mode os.FileMode) error {
	wantType := "regular file"
	typeOK := info.Mode().IsRegular()
	if directory {
		wantType = "directory"
		typeOK = info.IsDir()
	}
	if !typeOK || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a %s", subject, wantType)
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("%s must have mode 0%o", subject, mode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%s must be owned by the current uid", subject)
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("OAuth session has trailing JSON")
	}
	return nil
}

type savingTokenSource struct {
	mu      sync.Mutex
	source  oauth2.TokenSource
	config  *oauth2.Config
	current *oauth2.Token
	store   SessionStorage
	service string
}

// NewSavingTokenSource wraps SDK refresh so a new credential is durable before
// it is returned to the caller. If persistence fails, the refreshed token is
// not used.
func NewSavingTokenSource(source oauth2.TokenSource, config *oauth2.Config, initial *oauth2.Token, store SessionStorage, service string) oauth2.TokenSource {
	return &savingTokenSource{
		source:  source,
		config:  config,
		current: cloneToken(initial),
		store:   store,
		service: service,
	}
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.source == nil || s.config == nil || s.store == nil {
		return nil, errors.New("OAuth token source is incomplete")
	}
	token, err := s.source.Token()
	if err != nil {
		return nil, errors.New("refresh OAuth token")
	}
	if token == nil {
		return nil, errors.New("refresh OAuth token returned no token")
	}
	if !sameToken(s.current, token) {
		if err := s.store.Save(s.service, s.config, token); err != nil {
			return nil, fmt.Errorf("persist refreshed OAuth session: %w", err)
		}
		s.current = cloneToken(token)
	}
	return token, nil
}

func sameToken(a, b *oauth2.Token) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AccessToken == b.AccessToken && a.TokenType == b.TokenType &&
		a.RefreshToken == b.RefreshToken && a.Expiry.Equal(b.Expiry)
}

func cloneToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}
	copy := *token
	return &copy
}
