package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNightlyDue(t *testing.T) {
	at := func(s string) time.Time { t, _ := time.ParseInLocation("2006-01-02 15:04", s, time.Local); return t }
	assert.False(t, nightlyDue("2026-11-12", at("2026-11-13 03:59")))
	assert.True(t, nightlyDue("2026-11-12", at("2026-11-13 04:00")))
	assert.True(t, nightlyDue("2026-11-11", at("2026-11-13 09:30")))
	assert.False(t, nightlyDue("2026-11-13", at("2026-11-13 23:00")))
}
