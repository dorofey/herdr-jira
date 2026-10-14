package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// cacheMu keeps two tabs finishing at once from losing each other's save.
var cacheMu sync.Mutex

// lists.json keeps the last lists the picker loaded, by workspace, so the
// next popup can show them at once while Jira is asked again. What's shown
// is replaced as soon as the fresh answer arrives. It holds titles,
// identifiers, statuses and branch names: readable only by you, like the
// rest of the state dir, and a workspace's go when you sign out of it.
type listCache struct {
	Workspaces map[string]*cachedLists `json:"workspaces"`
}

// cachedLists are one workspace's. A list that was never loaded is nil,
// which an empty one isn't.
type cachedLists struct {
	Saved    time.Time  `json:"saved"`
	Mine     *[]issue   `json:"mine,omitempty"`
	Projects *[]project `json:"projects,omitempty"`
	Recent   *[]issue   `json:"recent,omitempty"`
}

func listCachePath() string { return filepath.Join(stateDir(), "lists.json") }

func readListCacheFile() listCache {
	var c listCache
	if data, err := os.ReadFile(listCachePath()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if c.Workspaces == nil {
		c.Workspaces = map[string]*cachedLists{}
	}
	return c
}

func writeListCacheFile(c listCache) {
	data, err := json.Marshal(c)
	if err != nil || os.MkdirAll(stateDir(), 0o700) != nil {
		return
	}
	tmp := listCachePath() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, listCachePath())
	}
}

// readListCache returns the lists workspace id had last time, if any.
func readListCache(id string) cachedLists {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if c := readListCacheFile().Workspaces[id]; c != nil {
		return *c
	}
	return cachedLists{}
}

// saveListCache stores one tab's list for workspace id, keeping the others.
func saveListCache(id string, t tab, issues []issue, projects []project) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	c := readListCacheFile()
	ws := c.Workspaces[id]
	if ws == nil {
		ws = &cachedLists{}
		c.Workspaces[id] = ws
	}
	// Never null in the file: an empty list is one that loaded.
	if issues == nil {
		issues = []issue{}
	}
	if projects == nil {
		projects = []project{}
	}
	switch t {
	case tabProjects:
		ws.Projects = &projects
	case tabRecent:
		ws.Recent = &issues
	default:
		ws.Mine = &issues
	}
	ws.Saved = time.Now()
	writeListCacheFile(c)
}

// dropListCache forgets workspace id's lists: you've signed out of it.
func dropListCache(id string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	c := readListCacheFile()
	if _, ok := c.Workspaces[id]; !ok {
		return
	}
	delete(c.Workspaces, id)
	if len(c.Workspaces) == 0 {
		_ = os.Remove(listCachePath())
		return
	}
	writeListCacheFile(c)
}
