//go:build !scheduler

package main

import "github.com/EvilFreelancer/coddy-agent/internal/config"

// jobDescription names a scheduled job for the header of its report. A build
// without the scheduler has no job files to read, and no run ever reports.
func jobDescription(cfg *config.Config, cwd, jobID string) string {
	return ""
}
