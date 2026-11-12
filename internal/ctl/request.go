package ctl

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	RequestTimeout = 15 * time.Second
	pollInterval   = 200 * time.Millisecond
	replySuffix    = ".reply.json"
)

var ErrNoAnswer = errors.New("deckhand did not answer; is it running?")

type Request struct {
	ID      string
	Op      string
	Caller  string
	Target  string
	Name    string
	Folder  string
	Account string
	Parent  string
	Text    string
}

type Reply struct {
	Err string
	Out string
}

func requestsDir(home string) string { return filepath.Join(home, "requests") }

func writeAtomic(dir, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func Send(home string, r Request, timeout time.Duration) (Reply, error) {
	dir := requestsDir(home)
	reqPath := filepath.Join(dir, r.ID+".json")
	replyPath := filepath.Join(dir, r.ID+replySuffix)
	if err := writeAtomic(dir, r.ID+".json", r); err != nil {
		return Reply{}, err
	}
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(replyPath)
		if err == nil {
			var rep Reply
			if err := json.Unmarshal(data, &rep); err != nil {
				os.Remove(replyPath)
				os.Remove(reqPath)
				return Reply{}, err
			}
			os.Remove(replyPath)
			os.Remove(reqPath)
			return rep, nil
		}
		if time.Now().After(deadline) {
			os.Remove(reqPath)
			return Reply{}, ErrNoAnswer
		}
		time.Sleep(pollInterval)
	}
}

func Pending(home string) []Request {
	dir := requestsDir(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type item struct {
		req Request
		mod time.Time
	}
	var items []item
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, replySuffix) || strings.HasPrefix(name, ".") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if _, err := os.Stat(filepath.Join(dir, id+replySuffix)); err == nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var r Request
		if json.Unmarshal(data, &r) != nil {
			continue
		}
		r.ID = id
		items = append(items, item{r, info.ModTime()})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	out := make([]Request, len(items))
	for i, it := range items {
		out[i] = it.req
	}
	return out
}

func Answer(home, id string, r Reply) error {
	return writeAtomic(requestsDir(home), id+replySuffix, r)
}
