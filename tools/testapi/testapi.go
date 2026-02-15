package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	CORS           bool              `json:"cors,omitempty"`
	DefaultHeaders map[string]string `json:"defaultHeaders,omitempty"`
	Routes         []Route           `json:"routes"`
}

type Route struct {
	Method   string    `json:"method"`             // GET|POST|PUT|PATCH (Default GET)
	Path     string    `json:"path"`               // exakte Pfadangabe, z.B. /fx/latest
	Mode     string    `json:"mode,omitempty"`     // first|round-robin|sequential (Default first)
	Responses []Reply  `json:"responses"`          // mind. 1 Eintrag
}

type Reply struct {
	Status      int               `json:"status,omitempty"`       // Default 200
	Headers     map[string]string `json:"headers,omitempty"`      // Optional
	ContentType string            `json:"contentType,omitempty"`  // Default: aus Body/Text abgeleitet
	Body        json.RawMessage   `json:"body,omitempty"`         // Gültiges JSON (roh, unverändert gesendet)
	Text        string            `json:"text,omitempty"`         // Falls kein JSON, wird Text gesendet
	DelayMs     int               `json:"delayMs,omitempty"`      // künstliche Latenz
}

type compiledRoute struct {
	method   string
	path     string
	mode     string
	replies  []Reply
	mu       sync.Mutex
	cursor   int // für round-robin/sequential
}

type server struct {
	cfg      Config
	routes   map[string]*compiledRoute // key: METHOD␟PATH
	defHdr   map[string]string
	cors     bool
}

func key(method, path string) string { return strings.ToUpper(method) + "\x1f" + path }

func loadConfig(fp string) (Config, error) {
	f, err := os.Open(fp)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("config parse error: %w", err)
	}
	if len(c.Routes) == 0 {
		return Config{}, errors.New("no routes defined")
	}
	// Defaults
	for i := range c.Routes {
		if strings.TrimSpace(c.Routes[i].Method) == "" {
			c.Routes[i].Method = "GET"
		}
		if strings.TrimSpace(c.Routes[i].Mode) == "" {
			c.Routes[i].Mode = "first"
		}
	}
	return c, nil
}

func compileConfig(c Config) (*server, error) {
	rmap := make(map[string]*compiledRoute, len(c.Routes))
	for _, r := range c.Routes {
		if r.Path == "" {
			return nil, fmt.Errorf("route with empty path")
		}
		if len(r.Responses) == 0 {
			return nil, fmt.Errorf("route %s %s has no responses", r.Method, r.Path)
		}
		mode := strings.ToLower(strings.TrimSpace(r.Mode))
		switch mode {
		case "first", "round-robin", "sequential":
		default:
			return nil, fmt.Errorf("route %s %s: unsupported mode %q", r.Method, r.Path, r.Mode)
		}
		cr := &compiledRoute{
			method:  strings.ToUpper(r.Method),
			path:    r.Path,
			mode:    mode,
			replies: r.Responses,
		}
		k := key(cr.method, cr.path)
		if _, exists := rmap[k]; exists {
			return nil, fmt.Errorf("duplicate route %s %s", cr.method, cr.path)
		}
		rmap[k] = cr
	}
	return &server{
		cfg:    c,
		routes: rmap,
		defHdr: c.DefaultHeaders,
		cors:   c.CORS,
	}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Health & config inspection
	if r.URL.Path == "/__health" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
		return
	}

	if s.cors {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
	}

	cr, ok := s.routes[key(r.Method, r.URL.Path)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	resp := s.pickReply(cr)

	// künstliche Latenz
	if d := resp.DelayMs; d > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(d)*time.Millisecond)
		defer cancel()
		select {
		case <-time.After(time.Duration(d) * time.Millisecond):
		case <-ctx.Done():
			http.Error(w, "request canceled", 499)
			return
		}
	}

	// Default-Header
	for k, v := range s.defHdr {
		if v != "" {
			w.Header().Set(k, v)
		}
	}
	// Antwort-Header
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	// Content-Type ableiten
	ct := strings.TrimSpace(resp.ContentType)
	switch {
	case ct != "":
		w.Header().Set("Content-Type", ct)
	case len(resp.Body) > 0:
		w.Header().Set("Content-Type", "application/json")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}

	status := resp.Status
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)

	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
		return
	}
	if resp.Text != "" {
		_, _ = w.Write([]byte(resp.Text))
		return
	}
	// fallback leerer Body
}

func (s *server) pickReply(cr *compiledRoute) Reply {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	switch cr.mode {
	case "first":
		return cr.replies[0]
	case "round-robin":
		r := cr.replies[cr.cursor%len(cr.replies)]
		cr.cursor++
		return r
	case "sequential":
		idx := cr.cursor
		if idx >= len(cr.replies) {
			idx = len(cr.replies) - 1
		}
		r := cr.replies[idx]
		if cr.cursor < len(cr.replies)-1 {
			cr.cursor++
		}
		return r
	default:
		return cr.replies[0]
	}
}

func main() {
	var (
		addr   = flag.String("addr", ":8080", "listen address (host:port)")
		cfg    = flag.String("config", "responses.json", "path to responses.json")
		quiet  = flag.Bool("quiet", false, "less logging")
	)
	flag.Parse()

	conf, err := loadConfig(*cfg)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	srv, err := compileConfig(conf)
	if err != nil {
		log.Fatalf("compile error: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", logMiddleware(*quiet, srv))

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("TestAPI listening on %s  (config=%s)", *addr, *cfg)
	if err := http.Serve(ln, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func logMiddleware(quiet bool, h http.Handler) http.Handler {
	if quiet {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		dur := time.Since(start)
		log.Printf("%s %s  (%s)", r.Method, r.URL.Path, dur.Truncate(time.Millisecond))
	})
}
