package main

import (
	"bytes"
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const manifestSchemaVersion = 1

// Manifest describes a portable WASIO instrument package.
type Manifest struct {
	Schema       int                  `toml:"schema"`
	Name         string               `toml:"name"`
	Version      string               `toml:"version"`
	Language     string               `toml:"language,omitempty"`
	Module       ManifestModule       `toml:"module"`
	Route        ManifestRoute        `toml:"route"`
	Metadata     ManifestMetadata     `toml:"metadata"`
	Examples     []ManifestExample    `toml:"examples,omitempty"`
	NativeRoutes []NativeRouteSpec    `toml:"native_routes,omitempty"`
	Capabilities ManifestCapabilities `toml:"capabilities"`
	Signature    *ManifestSignature   `toml:"signature,omitempty"`
}

type ManifestModule struct {
	Source string `toml:"source"`
	Build  string `toml:"build,omitempty"`
}

type ManifestRoute struct {
	Path      string   `toml:"path"`
	Methods   []string `toml:"methods,omitempty"`
	Cache     bool     `toml:"cache,omitempty"`
	TTL       int      `toml:"ttl,omitempty"`
	TimeoutMs int      `toml:"timeout_ms,omitempty"`
}

type ManifestMetadata struct {
	Category    string `toml:"category,omitempty"`
	Description string `toml:"description,omitempty"`
	UseCase     string `toml:"use_case,omitempty"`
}

type ManifestExample struct {
	Label string `toml:"label"`
	Query string `toml:"query"`
}

type ManifestCapabilities struct {
	Names        []string `toml:"names,omitempty"`
	NativeRoutes []string `toml:"native_routes,omitempty"`
}

type ManifestSignature struct {
	Algorithm string `toml:"algorithm,omitempty"`
	KeyID     string `toml:"key_id,omitempty"`
	PublicKey string `toml:"public_key,omitempty"`
	Value     string `toml:"value,omitempty"`
	SignedAt  string `toml:"signed_at,omitempty"`
}

func (m *Manifest) ApplyDefaults() {
	if m.Schema == 0 {
		m.Schema = manifestSchemaVersion
	}
	if strings.TrimSpace(m.Route.Path) == "" && strings.TrimSpace(m.Name) != "" {
		m.Route.Path = "/" + slugify(m.Name)
	}
	if strings.TrimSpace(m.Module.Source) == "" && strings.TrimSpace(m.Name) != "" {
		m.Module.Source = slugify(m.Name) + ".wasm"
	}
}

func (m *Manifest) Validate() error {
	m.ApplyDefaults()
	if m.Schema != manifestSchemaVersion {
		return fmt.Errorf("unsupported manifest schema %d", m.Schema)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("manifest.name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("manifest.version is required")
	}
	if strings.TrimSpace(m.Module.Source) == "" {
		return fmt.Errorf("manifest.module.source is required")
	}
	if strings.TrimSpace(m.Route.Path) == "" || !strings.HasPrefix(m.Route.Path, "/") {
		return fmt.Errorf("manifest.route.path must start with '/'")
	}
	return nil
}

func LoadManifest(path string) (*Manifest, error) {
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func LoadManifestFromBytes(data []byte) (*Manifest, error) {
	var m Manifest
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func SaveManifest(path string, m *Manifest) error {
	m.ApplyDefaults()
	if err := m.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func (m *Manifest) CanonicalCapabilityPayload() ([]byte, error) {
	names := append([]string(nil), m.Capabilities.Names...)
	nativeRoutes := append([]string(nil), m.Capabilities.NativeRoutes...)
	sort.Strings(names)
	sort.Strings(nativeRoutes)
	nativeRouteSpecs := append([]NativeRouteSpec(nil), m.NativeRoutes...)
	sort.Slice(nativeRouteSpecs, func(i, j int) bool {
		return nativeRouteSpecs[i].Path < nativeRouteSpecs[j].Path
	})
	type capabilityEnvelope struct {
		Schema       int      `json:"schema"`
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Route        string   `json:"route"`
		ModuleSource string   `json:"module_source"`
		Capabilities []string `json:"capabilities"`
		NativeRoutes []string `json:"native_routes,omitempty"`
		NativeSpecs  []NativeRouteSpec `json:"native_route_specs,omitempty"`
	}
	return json.Marshal(capabilityEnvelope{
		Schema:       m.Schema,
		Name:         m.Name,
		Version:      m.Version,
		Route:        m.Route.Path,
		ModuleSource: m.Module.Source,
		Capabilities: names,
		NativeRoutes: nativeRoutes,
		NativeSpecs:  nativeRouteSpecs,
	})
}

func (m *Manifest) SignCapabilities(privateKey ed25519.PrivateKey, keyID string) error {
	payload, err := m.CanonicalCapabilityPayload()
	if err != nil {
		return err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(privateKey, payload)
	m.Signature = &ManifestSignature{
		Algorithm: "ed25519",
		KeyID:     keyID,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Value:     base64.StdEncoding.EncodeToString(sig),
		SignedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	return nil
}

func (m *Manifest) VerifyCapabilities(publicKeyOverride []byte) error {
	if m.Signature == nil {
		return fmt.Errorf("manifest is unsigned")
	}
	if m.Signature.Algorithm != "" && m.Signature.Algorithm != "ed25519" {
		return fmt.Errorf("unsupported signature algorithm %q", m.Signature.Algorithm)
	}
	payload, err := m.CanonicalCapabilityPayload()
	if err != nil {
		return err
	}
	pub := publicKeyOverride
	if len(pub) == 0 {
		if strings.TrimSpace(m.Signature.PublicKey) == "" {
			return fmt.Errorf("manifest signature is missing public_key")
		}
		pub, err = base64.StdEncoding.DecodeString(strings.TrimSpace(m.Signature.PublicKey))
		if err != nil {
			return fmt.Errorf("decode public key: %w", err)
		}
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(m.Signature.Value))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, sig) {
		return fmt.Errorf("manifest capability signature verification failed")
	}
	return nil
}

func GenerateKeyPair() (publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, err error) {
	return ed25519.GenerateKey(crand.Reader)
}

func ReadKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("decode key file: %w", err)
	}
	return decoded, nil
}

func WriteKeyFile(path string, key []byte) error {
	return os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600)
}

func ManifestToRoute(m *Manifest, wasmPath string) Route {
	route := Route{
		WASMFile:    wasmPath,
		Cache:       m.Route.Cache,
		TTL:         m.Route.TTL,
		Description: m.Metadata.Description,
		Category:    m.Metadata.Category,
		UseCase:     m.Metadata.UseCase,
		Methods:     append([]string(nil), m.Route.Methods...),
		TimeoutMs:   m.Route.TimeoutMs,
		NativeRoutes: append([]NativeRouteSpec(nil), m.NativeRoutes...),
	}
	if len(m.Examples) > 0 {
		route.Example = m.Examples[0].Query
		route.Examples = make([]RouteExample, 0, len(m.Examples))
		for _, ex := range m.Examples {
			route.Examples = append(route.Examples, RouteExample{Label: ex.Label, Query: ex.Query})
		}
	}
	if len(m.Capabilities.Names) > 0 || len(m.Capabilities.NativeRoutes) > 0 {
		capabilities := append([]string{}, m.Capabilities.Names...)
		nativeRoutes := append([]string{}, m.Capabilities.NativeRoutes...)
		route.Config = map[string]interface{}{
			"manifest": map[string]interface{}{
				"capabilities":  capabilities,
				"native_routes": nativeRoutes,
			},
		}
	}
	return route
}

func DownloadBytes(rawURL string) ([]byte, error) {
	resp, err := http.Get(rawURL) //nolint:gosec // operator provided URL
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func ResolveManifestSource(base, ref string) (string, error) {
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		baseURL, err := url.Parse(base)
		if err != nil {
			return "", err
		}
		refURL, err := url.Parse(ref)
		if err != nil {
			return "", err
		}
		return baseURL.ResolveReference(refURL).String(), nil
	}
	if filepath.IsAbs(ref) {
		return ref, nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(base), ref)), nil
}

func InstallFilenameFromSource(source, fallbackName string) string {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		u, err := url.Parse(source)
		if err == nil {
			base := filepath.Base(u.Path)
			if strings.HasSuffix(base, ".wasm") {
				return base
			}
		}
	} else {
		base := filepath.Base(source)
		if strings.HasSuffix(base, ".wasm") {
			return base
		}
	}
	return slugify(fallbackName) + ".wasm"
}

func StoreManifestCopy(destRoot string, m *Manifest) (string, error) {
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(destRoot, fmt.Sprintf("%s@%s.toml", slugify(m.Name), m.Version))
	return path, SaveManifest(path, m)
}

func slugify(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '/' || r == '_' || r == '-' || r == ' ' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "instrument"
	}
	return out
}
