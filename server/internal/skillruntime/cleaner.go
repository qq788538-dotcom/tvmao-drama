package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type WorkspaceCleaner struct {
	root      string
	now       func() time.Time
	removeAll func(string) error
}

func NewWorkspaceCleaner(root string) *WorkspaceCleaner {
	return &WorkspaceCleaner{root: root, now: time.Now, removeAll: os.RemoveAll}
}

func (c *WorkspaceCleaner) Cleanup(now time.Time) error {
	if c == nil || c.root == "" {
		return nil
	}
	entries, err := os.ReadDir(c.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	loc := now.Location()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	cutoff := midnight.AddDate(0, 0, -3)
	removeAll := c.removeAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	var errs []error
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !isDateDirectoryName(entry.Name()) {
			continue
		}
		date, parseErr := time.ParseInLocation("20060102", entry.Name(), loc)
		if parseErr != nil || !date.Before(cutoff) {
			continue
		}
		path := filepath.Join(c.root, entry.Name())
		if removeErr := removeAll(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove expired Skill Run directory %s: %w", path, removeErr))
		}
	}
	return errors.Join(errs...)
}

func (c *WorkspaceCleaner) Run(ctx context.Context) {
	if c == nil {
		return
	}
	nowFunc := c.now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	if err := c.Cleanup(nowFunc()); err != nil {
		log.Printf("Skill Run workspace startup cleanup: %v", err)
	}
	for {
		now := nowFunc()
		next := nextWorkspaceCleanup(now)
		timer := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
			if err := c.Cleanup(nowFunc()); err != nil {
				log.Printf("Skill Run workspace scheduled cleanup: %v", err)
			}
		}
	}
}

func nextWorkspaceCleanup(now time.Time) time.Time {
	loc := now.Location()
	next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, loc)
	if !now.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func isDateDirectoryName(name string) bool {
	if len(name) != 8 {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
