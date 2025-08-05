package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"gopkg.in/robfig/cron.v2"

	"github.com/openshift/ci-tools/pkg/api"
)

// Global set to track used cron schedules
var usedCronSchedules = make(map[string]bool)

// generateUniqueCronSchedule creates a unique cron schedule that doesn't conflict with existing ones
func generateUniqueCronSchedule(baseCron string) (string, error) {
	// Handle predefined schedules first
	switch strings.ToLower(baseCron) {
	case "@daily":
		baseCron = "0 0 * * *"
	case "@weekly":
		baseCron = "0 0 * * 0"
	case "@monthly":
		baseCron = "0 0 1 * *"
	case "@yearly", "@annually":
		baseCron = "0 0 1 1 *"
	case "@hourly":
		baseCron = "0 * * * *"
	}

	// Parse the base cron expression to validate it
	_, err := cron.Parse(baseCron)
	if err != nil {
		return "", fmt.Errorf("invalid cron expression %s: %v", baseCron, err)
	}

	// Split the cron expression into fields
	fields := strings.Fields(baseCron)
	if len(fields) != 5 {
		return "", fmt.Errorf("expected 5 fields in cron expression, got %d", len(fields))
	}

	// Try different combinations until we find a unique one
	maxAttempts := 100
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Generate random offsets
		offsetMinutes := rand.Intn(60)
		offsetHours := rand.Intn(24)

		// For weekly schedules, also randomize the day of week
		dayOfWeek := fields[4]
		if fields[4] == "0" { // Weekly schedule (Sunday)
			dayOfWeek = strconv.Itoa(rand.Intn(7)) // Random day 0-6
		}

		// Create new cron expression with offsets
		newCron := fmt.Sprintf("%d %d %s %s %s",
			offsetMinutes,
			offsetHours,
			fields[2], // day of month
			fields[3], // month
			dayOfWeek, // day of week (randomized for weekly)
		)

		// Validate the new cron expression
		_, err := cron.Parse(newCron)
		if err != nil {
			continue // Try again with different offsets
		}

		// Check if this schedule is already used
		if !usedCronSchedules[newCron] {
			usedCronSchedules[newCron] = true
			return newCron, nil
		}
	}

	return "", fmt.Errorf("could not generate unique cron schedule after %d attempts", maxAttempts)
}

// convertIntervalToCron converts an interval string to a unique cron expression
func convertIntervalToCron(interval string) (string, error) {
	duration, err := time.ParseDuration(interval)
	if err != nil {
		return "", fmt.Errorf("invalid interval: %v", err)
	}

	// Convert duration to approximate cron expression
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60

	var baseCron string
	if hours >= 24 {
		days := hours / 24
		if days == 1 {
			baseCron = "0 0 * * *" // daily
		} else if days >= 6 && days <= 8 {
			// Treat anything close to a week (6-8 days) as weekly
			baseCron = "0 0 * * 0" // weekly
		} else if days >= 28 && days <= 31 {
			// Treat anything close to a month (28-31 days) as monthly
			baseCron = "0 0 1 * *" // monthly
		} else {
			// For other intervals, use a simple hourly pattern
			baseCron = fmt.Sprintf("0 */%d * * *", hours)
		}
	} else if hours > 0 {
		baseCron = fmt.Sprintf("0 */%d * * *", hours)
	} else if minutes > 0 {
		baseCron = fmt.Sprintf("*/%d * * * *", minutes)
	} else {
		baseCron = "0 * * * *" // default to hourly
	}

	// Generate a unique cron schedule from the base
	return generateUniqueCronSchedule(baseCron)
}

// extractVersionFromFilename extracts version from filename
func extractVersionFromFilename(filename string) (string, error) {
	// Look for patterns like "4.18", "4.19", etc. in the filename
	// Common patterns: openshift-4.18.yaml, 4.18-vsphere.yaml, etc.
	re := regexp.MustCompile(`(\d+\.\d+)`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) < 2 {
		return "", fmt.Errorf("no version found in filename: %s", filename)
	}
	return matches[1], nil
}

// isVersionLessThanOrEqual checks if version is <= target
func isVersionLessThanOrEqual(version, target string) bool {
	// Simple string comparison for major.minor versions
	// This works for versions like "4.18", "4.19", etc.
	return version <= target
}

// generateWeeklyCron generates a random weekly cron schedule
func generateWeeklyCron() (string, error) {
	// Generate random minute, hour, and day of week
	minutes := rand.Intn(60)
	hours := rand.Intn(24)
	dayOfWeek := rand.Intn(7) // 0-6 (Sunday-Saturday)

	cronExpr := fmt.Sprintf("%d %d * * %d", minutes, hours, dayOfWeek)

	// Ensure it's unique
	if !usedCronSchedules[cronExpr] {
		usedCronSchedules[cronExpr] = true
		return cronExpr, nil
	}

	// If not unique, try again
	return generateUniqueCronSchedule("0 0 * * 0")
}

// validateCronExpression validates a cron expression
func validateCronExpression(cronExpr string) error {
	// Handle predefined schedules
	switch strings.ToLower(cronExpr) {
	case "@daily", "@weekly", "@monthly", "@yearly", "@annually", "@hourly":
		return nil
	}

	// Try to parse the cron expression
	_, err := cron.Parse(cronExpr)
	return err
}

func main() {
	configDirName := os.Args[1]

	// Initialize random seed for consistent offsets
	rand.Seed(time.Now().UnixNano())

	// Read all entries in the specified directory.
	entries, err := os.ReadDir(configDirName)
	if err != nil {
		log.Fatalf("Failed to read directory: %v", err)
	}

	// Loop through each entry in the directory.
	for _, entry := range entries {
		// Skip directories.
		if entry.IsDir() {
			continue
		}

		// Get the file name.
		fileName := entry.Name()

		fullPath := ""
		// Check if the file has a .yaml or .yml extension.
		if strings.HasSuffix(fileName, ".yaml") || strings.HasSuffix(fileName, ".yml") {
			// Construct the full path and add it to the slice.
			fullPath = filepath.Join(configDirName, fileName)
		} else {
			continue
		}

		fileContents, err := os.ReadFile(fullPath)

		var releaseBuildConfiguration api.ReleaseBuildConfiguration

		if err != nil {
			log.Fatal(err)
		}

		if err := yaml.Unmarshal(fileContents, &releaseBuildConfiguration); err != nil {
			log.Fatal(err)
		}

		// Extract version from filename
		version, err := extractVersionFromFilename(fileName)
		if err != nil {
			log.Printf("Warning: could not extract version from filename %s: %v", fileName, err)
			version = "4.99" // Default to high version if can't extract
		}

		for n, t := range releaseBuildConfiguration.Tests {
			if strings.Contains(t.As, "vsphere") {
				// Skip yearly tests regardless of version
				if t.Cron != nil && strings.Contains(*t.Cron, "yearly") {
					continue
				}

				// For versions 4.14 and lower, force all tests to run weekly (except yearly)
				if isVersionLessThanOrEqual(version, "4.14") {
					weeklyCron, err := generateWeeklyCron()
					if err != nil {
						log.Printf("Warning: could not generate weekly cron for test %s: %v", t.As, err)
						continue
					}
					releaseBuildConfiguration.Tests[n].Cron = &weeklyCron
					if t.Interval != nil {
						releaseBuildConfiguration.Tests[n].Interval = nil
					}
					fmt.Printf("Modified %s (v%s): forced weekly -> %s\n", t.As, version, weeklyCron)
					continue
				}

				// Handle techpreview tests for versions 4.18 and lower (but > 4.14)
				if strings.Contains(strings.ToLower(t.As), "techpreview") && isVersionLessThanOrEqual(version, "4.18") {
					// Force weekly schedule for techpreview tests on older versions
					weeklyCron, err := generateWeeklyCron()
					if err != nil {
						log.Printf("Warning: could not generate weekly cron for techpreview test %s: %v", t.As, err)
						continue
					}
					releaseBuildConfiguration.Tests[n].Cron = &weeklyCron
					if t.Interval != nil {
						releaseBuildConfiguration.Tests[n].Interval = nil
					}
					fmt.Printf("Modified techpreview %s (v%s): forced weekly -> %s\n", t.As, version, weeklyCron)
					continue
				}

				// Convert interval to cron if present
				if t.Interval != nil {
					uniqueCron, err := convertIntervalToCron(*t.Interval)
					if err != nil {
						log.Printf("Warning: could not convert interval %s to cron: %v", *t.Interval, err)
						continue
					}
					releaseBuildConfiguration.Tests[n].Cron = &uniqueCron
					releaseBuildConfiguration.Tests[n].Interval = nil
					fmt.Printf("Modified %s: interval %s -> cron %s\n", t.As, *t.Interval, uniqueCron)
				} else if t.Cron != nil {
					// Validate the original cron expression
					if err := validateCronExpression(*t.Cron); err != nil {
						log.Printf("Warning: invalid cron expression %s: %v", *t.Cron, err)
						continue
					}

					uniqueCron, err := generateUniqueCronSchedule(*t.Cron)
					if err != nil {
						log.Printf("Warning: could not generate unique cron for %s: %v", *t.Cron, err)
						continue
					}

					releaseBuildConfiguration.Tests[n].Cron = &uniqueCron
					fmt.Printf("Modified %s: %s -> %s\n", t.As, *t.Cron, uniqueCron)
				}
			}
		}
		changed, err := yaml.Marshal(releaseBuildConfiguration)
		if err != nil {
			log.Fatal(err)
		}

		if err := os.WriteFile(fullPath, changed, 0644); err != nil {
			log.Fatal(err)
		}
	}
}
