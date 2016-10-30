/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caicloud/conf-watcher/app/options"
	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"golang.org/x/net/context"
)

const (
	RetryIntervalOnAddWatchEntry = 1 * time.Minute
)

// The format of an entry in a watchlist json file is:
//	[
//		{ "watchFile": "/var/run/flannel/subnet.env", "command": "service docker restart"},
//		...
//	]
type Entry struct {
	WatchFile string `json:"watchFile"`
	Command   string `json:"command"`
}

type watchEntry struct {
	watchlist string // entry is owned by watchlist file
	entry     *Entry
}

type recordEntry struct {
	lock       *sync.Mutex
	watchlist  string
	watchFiles []string // record all WatchFiles in each watchlist file
}

type Watcher struct {
	watchlistDir string
	dirWatcher   *fsnotify.Watcher
	fileWatcher  *fsnotify.Watcher
	retry        chan bool
	qlock        *sync.Mutex            // lock for queue
	queue        map[string]*watchEntry // queued entries which need to retry to watch
	mlock        *sync.Mutex            // lock for mapping
	mapping      map[string]*watchEntry // entries are being watching
	rlock        *sync.Mutex            // lock for record
	record       map[string]*recordEntry
}

func NewWatcher(config *options.WatcherConfig) *Watcher {
	return &Watcher{
		watchlistDir: config.WatchlistDir,
		dirWatcher:   nil,
		fileWatcher:  nil,
		retry:        make(chan bool),
		qlock:        &sync.Mutex{},
		queue:        make(map[string]*watchEntry),
		mlock:        &sync.Mutex{},
		mapping:      make(map[string]*watchEntry),
		rlock:        &sync.Mutex{},
		record:       make(map[string]*recordEntry),
	}
}

func (w *Watcher) addMapping(wentry *watchEntry) {
	w.mlock.Lock()
	w.mapping[wentry.entry.WatchFile] = wentry
	w.mlock.Unlock()
	glog.V(4).Infof("Added watchEntry '%p', Entry '%v' into mapping", wentry, *(wentry.entry))
}

func (w *Watcher) removeMapping(wentry *watchEntry) {
	w.mlock.Lock()
	defer w.mlock.Unlock()
	if entry, found := w.mapping[wentry.entry.WatchFile]; found {
		if entry == wentry {
			delete(w.mapping, wentry.entry.WatchFile)
			glog.V(4).Infof("Removed watchEntry '%p', Entry '%v' from mapping", wentry, *(wentry.entry))
			return
		}
	}

	glog.Warningf("Entry '%v' is already removed from mapping", *(wentry.entry))
}

func (w *Watcher) addQueue(wentry *watchEntry) {
	w.qlock.Lock()
	w.queue[wentry.entry.WatchFile] = wentry
	w.qlock.Unlock()
	glog.V(4).Infof("Added watchEntry '%p', Entry '%v' into queue to wait to retry", wentry, *(wentry.entry))
}

func (w *Watcher) removeQueue(wentry *watchEntry) {
	w.qlock.Lock()
	defer w.qlock.Unlock()
	if entry, found := w.queue[wentry.entry.WatchFile]; found {
		if entry == wentry {
			delete(w.queue, wentry.entry.WatchFile)
			glog.V(4).Infof("Removed watchEntry '%p', Entry '%v' from queue", wentry, *(wentry.entry))
			return
		}
	}

	glog.Warningf("Entry '%v' is already removed from queue", *(wentry.entry))
}

func (w *Watcher) addRecord(rentry *recordEntry) {
	w.rlock.Lock()
	w.record[rentry.watchlist] = rentry
	w.rlock.Unlock()
	glog.V(4).Infof("Added recordEntry '%p, %v' into record", rentry, *rentry)
}

func (w *Watcher) removeRecord(rentry *recordEntry) {
	w.rlock.Lock()
	defer w.rlock.Unlock()
	if entry, found := w.record[rentry.watchlist]; found {
		if entry == rentry {
			delete(w.record, rentry.watchlist)
			glog.V(4).Infof("Removed recordEntry '%p, %v' from record", rentry, *rentry)
			return
		}
	}

	glog.Warningf("recordEntry '%p, %v' is already removed from record", rentry, *rentry)
}

func (w *Watcher) addWatchEntry(wentry *watchEntry) error {
	glog.V(4).Infof("Adding watchEntry: %p, Entry: %v", wentry, *(wentry.entry))
	w.addMapping(wentry)
	if err := w.watch(wentry.entry.WatchFile); err != nil {
		glog.Errorf("Failed to Watch (watchFile: %s, watchlist: %s): %v", wentry.entry.WatchFile, wentry.watchlist, err)
		w.removeMapping(wentry)
		w.addQueue(wentry)
		return err
	}
	return nil
}

func (w *Watcher) removeWatchEntry(wentry *watchEntry) {
	glog.V(4).Infof("Removing watchEntry: %p, Entry: %v", wentry, *(wentry.entry))
	if err := w.removeWatch(wentry.entry.WatchFile); err != nil {
		glog.Errorf("Failed to remove watchEntry '%p', Entry '%v': %v", wentry, *(wentry.entry), err)
	}
	w.removeMapping(wentry)
	// When removeWatch return error, wentry may be moved to queue
	w.removeQueue(wentry)
}

func (w *Watcher) moveWatchEntryToQueue(wentry *watchEntry) {
	glog.V(4).Infof("Moving watchEntry '%p', Entry '%v' from mapping to queue", wentry, *(wentry.entry))
	// Can't remove non-existent inotify watch
	//if err := w.removeWatch(wentry.entry.WatchFile); err != nil {
	//	glog.Errorf("Failed to remove watchFile '%s': %v", wentry.entry.WatchFile, err)
	//}
	w.removeMapping(wentry)
	w.addQueue(wentry)
}

func isDir(filename string) (bool, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return false, err
	}
	if fi.IsDir() {
		return true, nil
	} else {
		return false, nil
	}
}

func fileExist(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
}

// Add watchlist directory to watch
func (w *Watcher) addWatchlistDir(dirname string) {
	glog.Infof("Adding watchlist directory: %s", dirname)
	bl, err := isDir(dirname)
	if err != nil || bl == false {
		glog.Errorf("'%s' is not a directory but a file", dirname)
		return
	}

	w.watchDir(dirname)
}

// Add entries in the watchlist file to watch
func (w *Watcher) addWatchlistEntry(watchlist string) {
	// If an old watchlist is already in record, removed first
	w.removeWatchlistEntry(watchlist)

	glog.V(4).Infof("Adding entries from watchlist '%s'", watchlist)
	bl, err := isDir(watchlist)
	if err != nil || bl == true {
		glog.Errorf("'%s' is not a watchlist file but a directory", watchlist)
		return
	}

	file, err := os.Open(watchlist)
	defer file.Close()
	if err != nil {
		glog.Errorf("Couldn't open watchlist file '%s': %v", watchlist, err)
		return
	}

	entries := []Entry{}
	if err = json.NewDecoder(file).Decode(&entries); err != nil {
		glog.Errorf("Failed to decode entries from '%s': %v", watchlist, err)
		return
	}
	glog.V(4).Infof("Watchlist '%s' entries: %v are decoded", watchlist, entries)

	var rentry *recordEntry

	if len(entries) > 0 {
		rentry = new(recordEntry)
		rentry.watchlist = watchlist
		rentry.lock = &sync.Mutex{}

		rentry.lock.Lock()
		defer rentry.lock.Unlock()

		w.addRecord(rentry)

		for _, en := range entries {
			if en.WatchFile == "" {
				glog.Errorf("WatchFile is empty in entry: %v, watchlist file: %s", en, watchlist)
				continue
			}

			rentry.watchFiles = append(rentry.watchFiles, en.WatchFile)

			newEntry := new(Entry)
			newEntry.WatchFile = en.WatchFile
			newEntry.Command = en.Command
			wentry := &watchEntry{
				watchlist: watchlist,
				entry:     newEntry,
			}
			if err := w.addWatchEntry(wentry); err == nil {
				w.executeCommand(wentry.entry.WatchFile)
			}
		}
	}
}

// Remove watchEntries belong to the watchlist file
func (w *Watcher) removeWatchlistEntry(watchlist string) {
	glog.V(4).Infof("Removing entries from watchlist '%s'", watchlist)
	if rentry, wlFound := w.record[watchlist]; wlFound {
		for _, watchFile := range rentry.watchFiles {
			if entry, ok := w.mapping[watchFile]; ok {
				w.removeWatchEntry(entry)
			}
		}
		w.removeRecord(rentry)
		return
	}

	glog.Infof("Entries from watchlist '%s' is already removed", watchlist)
}

func (w *Watcher) watchDir(dirname string) {
	if err := w.dirWatcher.Add(dirname); err != nil {
		if os.IsNotExist(err) {
			glog.Warningf("Couldn't watch directory '%s': %v", dirname, err)
		} else {
			glog.Errorf("Couldn't watch directory '%s': %v", dirname, err)
		}
	} else {
		glog.Infof("Watching directory: %s", dirname)
	}
}

func (w *Watcher) watch(watchFile string) error {
	if err := w.fileWatcher.Add(watchFile); err != nil {
		return err
	}

	glog.Infof("Watching file: %s", watchFile)
	return nil
}

func (w *Watcher) removeWatchDir(dirname string) error {
	if err := w.dirWatcher.Remove(dirname); err != nil {
		return err
	} else {
		glog.Infof("Removed watching directory: %s", dirname)
	}
	return nil
}

func (w *Watcher) removeWatch(watchFile string) error {
	if err := w.fileWatcher.Remove(watchFile); err != nil {
		return err
	} else {
		glog.Infof("Removed watchFile: %s", watchFile)
	}
	return nil
}

func (w *Watcher) executeCommand(watchFile string) {
	cmdString := w.mapping[watchFile].entry.Command
	if cmdString != "" {
		cmd := exec.Command("/bin/sh", "-c", cmdString)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			glog.Errorf("Failed to execute: /bin/sh -c \"%s\", error: %v", cmdString, err)
		} else {
			glog.Infof("Executed command: /bin/sh -c \"%s\", output: %s", cmdString, out.String())
		}
	}
}

func (w *Watcher) doFileEvent(ev fsnotify.Event) {
	glog.Infof("Catched file: %s", ev.String())

	if ev.Op&fsnotify.Create == fsnotify.Create || ev.Op&fsnotify.Write == fsnotify.Write {
		// When the config file from an entry in a watchlist file is modified, execute the Command.
		w.executeCommand(ev.Name)

	} else if ev.Op&fsnotify.Remove == fsnotify.Remove {
		if wentry, found := w.mapping[ev.Name]; found {
			w.moveWatchEntryToQueue(wentry)
		} else {
			glog.Errorf("'%s' is not found in mapping", ev.Name)
		}

	} else if ev.Op&fsnotify.Rename == fsnotify.Rename {
		// There are two cases for Rename event:
		// 1. ev.Name watchFile was renamed to another file
		// 2. another file was renamed to ev.Name
		if fileExist(ev.Name) {
			// For case 2: ev.Name file exists
			glog.Infof("Anather file is renamed to '%s'", ev.Name)
			if wentry, ok := w.queue[ev.Name]; ok {
				w.removeQueue(wentry)
				if err := w.addWatchEntry(wentry); err == nil {
					w.executeCommand(wentry.entry.WatchFile)
				}
			}
		} else {
			// For case 1: ev.Name file don't exist
			glog.Infof("'%s' is renamed to Anather file", ev.Name)
			if wentry, found := w.mapping[ev.Name]; found {
				w.moveWatchEntryToQueue(wentry)
			} else {
				glog.Errorf("'%s' is not found in mapping", ev.Name)
			}
		}
	}
}

func (w *Watcher) doDirEvent(ev fsnotify.Event) {
	if filepath.Ext(ev.Name) != ".json" {
		return
	}

	glog.Infof("Catched direcotry: %s", ev.String())

	// When a watchlist file is modified, reload the entries in the watchlist file
	if ev.Op&fsnotify.Create == fsnotify.Create || ev.Op&fsnotify.Write == fsnotify.Write {
		w.addWatchlistEntry(ev.Name)

	} else if ev.Op&fsnotify.Remove == fsnotify.Remove || ev.Op&fsnotify.Rename == fsnotify.Rename {
		w.removeWatchlistEntry(ev.Name)
	}
}

func (w *Watcher) startWatchFileWatching(ctx context.Context) {
	for {
		select {
		case ev := <-w.fileWatcher.Events:
			w.doFileEvent(ev)

		case err := <-w.fileWatcher.Errors:
			glog.Errorf("File watch error: %v", err)

		case <-ctx.Done():
			return
		}
	}
}

func (w *Watcher) startWatchlistWatching(ctx context.Context) {
	for {
		select {
		case ev := <-w.dirWatcher.Events:
			w.doDirEvent(ev)

		case err := <-w.dirWatcher.Errors:
			glog.Errorf("Directory watch error: %v", err)

		case <-ctx.Done():
			return
		}
	}
}

// Retry to add watchEntry in queue if watchFile exists
func (w *Watcher) retryAddWatchEntry(ctx context.Context) {
	for {
		select {
		case <-time.After(RetryIntervalOnAddWatchEntry):
			if len(w.queue) < 1 {
				continue
			}
			for k, wentry := range w.queue {
				glog.V(4).Infof("Getting element '%v' from queue: (watchEntry: %p, Entry: %v)", k, wentry, *(wentry.entry))
				w.removeQueue(wentry)
				if err := w.addWatchEntry(wentry); err == nil {
					// If watchFile exists, then execute it's command
					w.executeCommand(wentry.entry.WatchFile)
				}
			}
			glog.V(4).Infof("Record: %v, Mapping: %v, Queue: %v", w.record, w.mapping, w.queue)

		case <-ctx.Done():
			return
		}
	}
}

func (w *Watcher) init() error {
	if w.watchlistDir == "" {
		return fmt.Errorf("No watchlist direcotry specified by --watchlist-dir")
	}

	var err error
	if _, err = os.Stat(w.watchlistDir); os.IsNotExist(err) {
		return err
	}

	w.fileWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	w.dirWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	return nil
}

func (w *Watcher) clear() {
	if w.fileWatcher != nil {
		w.fileWatcher.Close()
		w.fileWatcher = nil
	}
	if w.dirWatcher != nil {
		w.dirWatcher.Close()
		w.dirWatcher = nil
	}

	glog.Flush()
}

// Load all the watchlist file in the watchlistDir
func (w *Watcher) loadWatchlist() {
	files, err := ioutil.ReadDir(w.watchlistDir)
	if err == nil {
		for _, f := range files {
			if f.IsDir() == false {
				s := []string{}
				s = append(s, w.watchlistDir)
				s = append(s, f.Name())
				w.addWatchlistEntry(strings.Join(s, "/"))
			}
		}
	} else {
		glog.Errorf("Failed to load watchlist file from directory '%s': %v", w.watchlistDir, err)
	}
}

func (w *Watcher) Run() {
	// Register for SIGINT and SIGTERM
	glog.Info("Installing signal handlers")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())

	if err := w.init(); err != nil {
		glog.Fatalf("Failed to init Watcher: %v", err)
		os.Exit(1)
	}

	wgrp := sync.WaitGroup{}
	wgrp.Add(1)
	go func() {
		w.startWatchFileWatching(ctx)
		wgrp.Done()
	}()

	wgrp.Add(1)
	go func() {
		w.startWatchlistWatching(ctx)
		wgrp.Done()
	}()

	wgrp.Add(1)
	go func() {
		w.retryAddWatchEntry(ctx)
		wgrp.Done()
	}()

	w.addWatchlistDir(w.watchlistDir)
	w.loadWatchlist()

	glog.Infof("Received signal: %s", <-sigs)
	signal.Stop(sigs)
	glog.Info("Exiting...")

	cancel()
	wgrp.Wait()
	w.clear()
}
