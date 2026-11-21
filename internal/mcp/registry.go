package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const RegistryURL = "https://registry.modelcontextprotocol.io/v0.1/servers"

type Input struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IsRequired  bool     `json:"isRequired"`
	IsSecret    bool     `json:"isSecret"`
	Default     string   `json:"default"`
	Value       string   `json:"value"`
	ValueHint   string   `json:"valueHint"`
	Type        string   `json:"type"`
	Choices     []string `json:"choices"`
}

type Package struct {
	RegistryType string `json:"registryType"`
	Identifier   string `json:"identifier"`
	Version      string `json:"version"`
	RuntimeHint  string `json:"runtimeHint"`
	Transport    struct {
		Type string `json:"type"`
	} `json:"transport"`
	RuntimeArguments     []Input `json:"runtimeArguments"`
	PackageArguments     []Input `json:"packageArguments"`
	EnvironmentVariables []Input `json:"environmentVariables"`
}

type Remote struct {
	Type    string  `json:"type"`
	URL     string  `json:"url"`
	Headers []Input `json:"headers"`
}

type Entry struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	WebsiteURL  string `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
	Packages []Package `json:"packages"`
	Remotes  []Remote  `json:"remotes"`
}

func (e Entry) Display() string {
	if e.Title != "" {
		return e.Title
	}
	return e.Name
}

type listResponse struct {
	Servers []struct {
		Server Entry `json:"server"`
	} `json:"servers"`
	Metadata struct {
		NextCursor string `json:"nextCursor"`
	} `json:"metadata"`
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

type Cache interface {
	CacheGet(ctx context.Context, key string) (string, bool)
	CacheSet(ctx context.Context, key, value string, ttl time.Duration)
}

var cache Cache

func SetCache(c Cache) { cache = c }

const searchTTL = 10 * time.Minute

type cachedPage struct {
	Entries []Entry `json:"entries"`
	Next    string  `json:"next"`
}

func Search(ctx context.Context, query, cursor string, limit int) ([]Entry, string, error) {
	key := fmt.Sprintf("mcp:search:%d:%s:%s", limit, strings.ToLower(strings.TrimSpace(query)), cursor)
	if cache != nil {
		if raw, ok := cache.CacheGet(ctx, key); ok {
			var p cachedPage
			if json.Unmarshal([]byte(raw), &p) == nil {
				return p.Entries, p.Next, nil
			}
		}
	}
	entries, next, err := search(ctx, query, cursor, limit)
	if err == nil && cache != nil {
		if raw, merr := json.Marshal(cachedPage{entries, next}); merr == nil {
			cache.CacheSet(ctx, key, string(raw), searchTTL)
		}
	}
	return entries, next, err
}

func search(ctx context.Context, query, cursor string, limit int) ([]Entry, string, error) {
	q := url.Values{}
	q.Set("version", "latest")
	q.Set("limit", fmt.Sprint(limit))
	if s := strings.TrimSpace(query); len([]rune(s)) >= 3 {
		q.Set("search", s)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, RegistryURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "seraph-mcp-manager")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("registry: %s", resp.Status)
	}
	var lr listResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, "", err
	}
	out := make([]Entry, 0, len(lr.Servers))
	for _, s := range lr.Servers {
		if len(Options(s.Server)) > 0 {
			out = append(out, s.Server)
		}
	}
	return out, lr.Metadata.NextCursor, nil
}

type Field struct {
	Kind        string
	Name        string
	Description string
	Required    bool
	Secret      bool
	Default     string
}

const (
	FieldEnv    = "env"
	FieldHeader = "header"
	FieldArg    = "arg"
	FieldURLVar = "url"
)

type Option struct {
	Label  string
	Server Server
	Fields []Field
}

func Options(e Entry) []Option {
	var out []Option
	for _, p := range e.Packages {
		if p.Transport.Type != "" && p.Transport.Type != TransportStdio {
			continue
		}
		if o, ok := packageOption(e, p); ok {
			out = append(out, o)
		}
	}
	for _, r := range e.Remotes {
		if o, ok := remoteOption(e, r); ok {
			out = append(out, o)
		}
	}
	return out
}

func base(e Entry) Server {
	return Server{
		Registry:    e.Name,
		Version:     e.Version,
		Description: e.Description,
		Claude:      true,
		Cursor:      true,
	}
}

func packageOption(e Entry, p Package) (Option, bool) {
	s := base(e)
	s.Transport = TransportStdio
	var fields []Field
	var pkgArgs []string
	for _, a := range p.PackageArguments {
		v := firstNonEmpty(a.Value, a.Default)
		if v == "" {
			if a.IsRequired {
				fields = append(fields, Field{Kind: FieldArg, Name: firstNonEmpty(a.Name, a.ValueHint, "argument"), Description: a.Description, Required: true, Secret: a.IsSecret})
			}
			continue
		}
		if a.Type == "named" && a.Name != "" {
			pkgArgs = append(pkgArgs, a.Name, v)
		} else {
			pkgArgs = append(pkgArgs, v)
		}
	}
	switch p.RegistryType {
	case "npm":
		s.Command = "npx"
		s.Args = append([]string{"-y", p.Identifier + versionSuffix("@", p.Version)}, pkgArgs...)
		s.Source = "npm:" + p.Identifier
	case "pypi":
		s.Command = "uvx"
		s.Args = append([]string{p.Identifier + versionSuffix("==", p.Version)}, pkgArgs...)
		s.Source = "pypi:" + p.Identifier
	case "oci":
		image := p.Identifier
		if !strings.Contains(image[strings.LastIndex(image, "/")+1:], ":") && p.Version != "" {
			image += ":" + p.Version
		}
		s.Command = "docker"
		s.Args = []string{"run", "-i", "--rm"}
		for _, v := range p.EnvironmentVariables {
			s.Args = append(s.Args, "-e", v.Name)
		}
		s.Args = append(append(s.Args, image), pkgArgs...)
		s.Source = "oci:" + p.Identifier
	default:
		return Option{}, false
	}
	for _, v := range p.EnvironmentVariables {
		fields = append(fields, Field{Kind: FieldEnv, Name: v.Name, Description: v.Description, Required: v.IsRequired, Secret: v.IsSecret, Default: v.Default})
	}
	label := p.RegistryType + " · " + s.Command
	return Option{Label: label, Server: s, Fields: fields}, true
}

var urlVar = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func remoteOption(e Entry, r Remote) (Option, bool) {
	s := base(e)
	switch r.Type {
	case "streamable-http", "http":
		s.Transport = TransportHTTP
	case "sse":
		s.Transport = TransportSSE
	default:
		return Option{}, false
	}
	s.URL = r.URL
	s.Source = "remote:" + r.URL
	var fields []Field
	for _, m := range urlVar.FindAllStringSubmatch(r.URL, -1) {
		fields = append(fields, Field{Kind: FieldURLVar, Name: m[1], Required: true})
	}
	for _, h := range r.Headers {
		fields = append(fields, Field{Kind: FieldHeader, Name: h.Name, Description: h.Description, Required: h.IsRequired, Secret: h.IsSecret, Default: h.Default})
	}
	return Option{Label: "remote · " + s.Transport, Server: s, Fields: fields}, true
}

func (o Option) Build(name string, values map[string]string) (Server, error) {
	s := o.Server
	s.Name = name
	s.Args = append([]string(nil), s.Args...)
	for _, f := range o.Fields {
		v := strings.TrimSpace(values[f.Name])
		if v == "" {
			v = f.Default
		}
		if v == "" {
			if f.Required {
				return Server{}, fmt.Errorf("%s is required", f.Name)
			}
			continue
		}
		switch f.Kind {
		case FieldEnv:
			if s.Env == nil {
				s.Env = map[string]string{}
			}
			s.Env[f.Name] = v
		case FieldHeader:
			if s.Headers == nil {
				s.Headers = map[string]string{}
			}
			s.Headers[f.Name] = v
		case FieldArg:
			s.Args = append(s.Args, v)
		case FieldURLVar:
			s.URL = strings.ReplaceAll(s.URL, "{"+f.Name+"}", v)
		}
		if f.Secret {
			s.Secrets = append(s.Secrets, f.Name)
		}
	}
	if s.Command == "docker" && len(s.Env) > 0 {
		var args []string
		for i := 0; i < len(s.Args); i++ {
			if s.Args[i] == "-e" && i+1 < len(s.Args) {
				if _, set := s.Env[s.Args[i+1]]; !set {
					i++
					continue
				}
			}
			args = append(args, s.Args[i])
		}
		s.Args = args
	}
	return s, nil
}

func versionSuffix(sep, v string) string {
	if v == "" || v == "latest" {
		return ""
	}
	return sep + v
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func Lookup(ctx context.Context, name string) (Entry, error) {
	u := RegistryURL + "/" + url.PathEscape(name) + "/versions/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Entry{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "seraph-mcp-manager")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Entry{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Entry{}, fmt.Errorf("no server %q in the registry", name)
	}
	if resp.StatusCode != http.StatusOK {
		return Entry{}, fmt.Errorf("registry: %s", resp.Status)
	}
	var r struct {
		Server Entry `json:"server"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Entry{}, err
	}
	return r.Server, nil
}
