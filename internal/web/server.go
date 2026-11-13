package web

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/benteven/deckhand/internal/tasks"
)

//go:embed static
var static embed.FS

const (
	cookieName  = "deckhand_token"
	headerName  = "X-Deckhand-Token"
	replyWait   = 15 * time.Second
	maxBodySize = 1 << 20
)

type View struct {
	Tasks   []TaskView    `json:"tasks"`
	Waiting []SessionView `json:"waiting"`
	Chat    []ChatLine    `json:"chat"`
	Count   int           `json:"count"`
}

type TaskView struct {
	tasks.Task
	Group       int      `json:"group"`
	ProjectName string   `json:"project_name,omitempty"`
	Stage       string   `json:"stage,omitempty"`
	Menu        []string `json:"menu,omitempty"`
	Screen      string   `json:"screen,omitempty"`
}

type SessionView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Need string `json:"need"`
}

type ChatLine struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`
	Text string    `json:"text"`
}

type Action struct {
	Kind, Task, Session, Text, Label string
	Choice                           int
	Reply                            chan error
}

type Server struct {
	port    int
	token   string
	act     func(Action)
	mux     *http.ServeMux
	srv     *http.Server
	clients atomic.Int64

	mu   sync.Mutex
	view []byte
	wake chan struct{}
}

func New(port int, token string, act func(Action)) *Server {
	s := &Server{port: port, token: token, act: act, mux: http.NewServeMux(), wake: make(chan struct{})}
	s.view, _ = json.Marshal(View{})
	files, _ := fs.Sub(static, "static")
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(files)))
	s.mux.HandleFunc("GET /api/view", s.getView)
	s.mux.HandleFunc("GET /api/events", s.events)
	for _, kind := range []string{"answer", "choice", "open", "chat", "done"} {
		s.mux.HandleFunc("POST /api/"+kind, s.action(kind))
	}
	s.srv = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: s, ReadHeaderTimeout: 10 * time.Second}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != fmt.Sprintf("127.0.0.1:%d", s.port) && r.Host != fmt.Sprintf("localhost:%d", s.port) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") && !s.allowed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) allowed(r *http.Request) bool {
	if s.match(r.Header.Get(headerName)) {
		return true
	}
	if r.Method != http.MethodGet {
		return false
	}
	c, err := r.Cookie(cookieName)
	return err == nil && s.match(c.Value)
}

func (s *Server) match(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if t := r.URL.Query().Get("token"); s.match(t) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	page, err := static.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	w.Write(page)
}

func (s *Server) current() ([]byte, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view, s.wake
}

func (s *Server) getView(w http.ResponseWriter, r *http.Request) {
	data, _ := s.current()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	s.clients.Add(1)
	defer s.clients.Add(-1)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	for {
		data, wake := s.current()
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-wake:
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) action(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Task    string `json:"task"`
			Session string `json:"session"`
			Text    string `json:"text"`
			Label   string `json:"label"`
			Choice  int    `json:"choice"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		reply := make(chan error, 1)
		s.act(Action{Kind: kind, Task: body.Task, Session: body.Session, Text: body.Text, Label: body.Label, Choice: body.Choice, Reply: reply})
		select {
		case err := <-reply:
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case <-time.After(replyWait):
			http.Error(w, "deckhand did not answer", http.StatusGatewayTimeout)
		case <-r.Context().Done():
		}
	}
}

func (s *Server) Publish(v View) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = data
	close(s.wake)
	s.wake = make(chan struct{})
}

func (s *Server) Clients() int {
	return int(s.clients.Load())
}

func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Close() error {
	return s.srv.Close()
}

func URL(port int, token string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/?token=%s", port, token)
}

func Token(home string) (string, error) {
	path := filepath.Join(home, "web-token")
	if data, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t, nil
		}
		return "", fmt.Errorf("%s is empty; delete it and try again", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return Token(home)
	}
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(token)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return token, nil
}
