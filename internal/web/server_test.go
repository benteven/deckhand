package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuard(t *testing.T) {
	s := New(7317, "secret", func(a Action) { a.Reply <- nil })
	do := func(method, host string, prep func(*http.Request)) int {
		r := httptest.NewRequest(method, "/api/"+map[string]string{"GET": "view", "POST": "answer"}[method], nil)
		r.Host = host
		if prep != nil {
			prep(r)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	header := func(r *http.Request) { r.Header.Set("X-Deckhand-Token", "secret") }
	cookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "deckhand_token", Value: "secret"}) }
	assert.Equal(t, http.StatusForbidden, do("GET", "127.0.0.1:7317", nil))
	assert.Equal(t, http.StatusOK, do("GET", "127.0.0.1:7317", header))
	assert.Equal(t, http.StatusForbidden, do("GET", "evil.example", header))
	assert.Equal(t, http.StatusForbidden, do("POST", "127.0.0.1:7317", cookie))
}
