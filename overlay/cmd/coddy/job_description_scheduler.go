//go:build scheduler

package main

import (
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// jobDescription names a scheduled job for the header of its report: the
// description its own file carries, looked up among the user jobs first and then
// among the project jobs of the workspace. A job whose file cannot be read still
// reports - headed as an unnamed job rather than silently dropped.
func jobDescription(cfg *config.Config, cwd, jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	roots := make([]string, 0, 2)
	if cfg != nil {
		roots = append(roots, cfg.SchedulerUserDir())
	}
	if strings.TrimSpace(cwd) != "" {
		roots = append(roots, filepath.Join(cwd, config.SchedulerProjectDir))
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		fm, _, err := storage.ParseJobFile(filepath.Join(root, jobID+".md"))
		if err != nil || fm == nil {
			continue
		}
		if d := strings.TrimSpace(fm.Description); d != "" {
			return d
		}
	}

	return ""
}
