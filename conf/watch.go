package conf

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch is retained for callers that do not need error-aware application.
func (p *Conf) Watch(filePath, xDnsPath, sDnsPath string, reload func()) error {
	return p.WatchContext(context.Background(), filePath, xDnsPath, sDnsPath, func(candidate *Conf) error {
		*p = *candidate
		reload()
		return nil
	})
}

// Watch directories so atomic file replacement does not detach the watcher.
// Only commit candidate configuration after application succeeds.
func (p *Conf) WatchContext(ctx context.Context, filePath, xDnsPath, sDnsPath string, apply func(*Conf) error) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new watcher: %w", err)
	}
	files, dirs := make(map[string]bool), make(map[string]bool)
	for _, path := range []string{filePath, xDnsPath, sDnsPath} {
		if path == "" {
			continue
		}
		path, err = filepath.Abs(path)
		if err != nil {
			watcher.Close()
			return err
		}
		files[filepath.Clean(path)] = true
		dir := filepath.Dir(path)
		if !dirs[dir] {
			if err := watcher.Add(dir); err != nil {
				watcher.Close()
				return fmt.Errorf("watch directory: %w", err)
			}
			dirs[dir] = true
		}
	}
	go func() {
		defer watcher.Close()
		var timer *time.Timer
		var fire <-chan time.Time
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !files[filepath.Clean(event.Name)] || event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
					continue
				}
				if timer == nil {
					timer = time.NewTimer(250 * time.Millisecond)
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(250 * time.Millisecond)
				}
				fire = timer.C
			case <-fire:
				fire = nil
				candidate := New()
				if err := candidate.LoadFromPath(filePath); err != nil {
					log.Printf("reload config rejected; keeping active config: %s", err)
					continue
				}
				if err := apply(candidate); err != nil {
					log.Printf("reload application failed; keeping previous config: %s", err)
					continue
				}
				*p = *candidate
				log.Println("reload config success")
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("file watcher error: %s", err)
			}
		}
	}()
	return nil
}
