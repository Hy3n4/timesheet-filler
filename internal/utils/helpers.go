package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func GenerateFileToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("Error generating random token: %v", err)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func ParseMonth(monthStr string) (int, error) {
	monthStr = strings.TrimSpace(strings.ToLower(monthStr))
	months := map[string]int{
		"january":   1,
		"february":  2,
		"march":     3,
		"april":     4,
		"may":       5,
		"june":      6,
		"july":      7,
		"august":    8,
		"september": 9,
		"october":   10,
		"november":  11,
		"december":  12,
	}
	if month, ok := months[monthStr]; ok {
		return month, nil
	}
	// Try to parse as integer
	if monthNum, err := strconv.Atoi(monthStr); err == nil && monthNum >= 1 && monthNum <= 12 {
		return monthNum, nil
	}
	return 0, fmt.Errorf("invalid month: %s", monthStr)
}

func SplitName(fullName string) (firstname, lastname string) {
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return "", ""
	}
	lastname = parts[0]
	if len(parts) > 1 {
		firstname = strings.Join(parts[1:], " ")
	}
	return firstname, lastname
}

func SafeGetCellValue(row []string, index int) string {
	if index < len(row) {
		return row[index]
	}
	return ""
}

func ParseDateTime(input string) (time.Time, error) {
	layout := "2006-01-02 15:04"
	return time.Parse(layout, input)
}

func ParseDate(input string) (time.Time, error) {
	layout := "2006-01-02 15:04"
	return time.Parse(layout, input)
}

func RemoveDiacritics(s string) string {
	t := make([]rune, 0, len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			// Skip non-spacing marks (diacritics)
			continue
		}
		t = append(t, r)
	}
	return string(t)
}

func SanitizeFilename(filename string) string {
	// Replace any invalid characters with underscores
	invalidChars := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	filename = invalidChars.ReplaceAllString(filename, "_")
	// Trim spaces and periods at the end
	filename = strings.TrimRight(filename, " .")
	return filename
}

func GenerateToken() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		log.Printf("Error generating token: %v", err)
		return ""
	}
	return hex.EncodeToString(b)
}

func TimeToSerial(hours, minutes, seconds int) float64 {
	totalSeconds := hours*3600 + minutes*60 + seconds
	secondsInDay := 24 * 3600
	return float64(totalSeconds) / float64(secondsInDay)
}
