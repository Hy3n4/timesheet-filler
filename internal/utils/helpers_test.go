package utils

import (
	"testing"
)

func TestGenerateFileToken(t *testing.T) {
	token1 := GenerateFileToken()
	token2 := GenerateFileToken()

	if token1 == "" {
		t.Error("GenerateFileToken returned an empty string")
	}

	if token1 == token2 {
		t.Error("GenerateFileToken should return different tokens each time")
	}

	if len(token1) != 32 {
		t.Errorf("Expected token length to be 32, got %d", len(token1))
	}
}

func TestSplitName(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantFirstname string
		wantLastname  string
	}{
		{
			name:          "normal name",
			input:         "Novak Jan",
			wantFirstname: "Jan",
			wantLastname:  "Novak",
		},
		{
			name:          "single name",
			input:         "SingleName",
			wantFirstname: "",
			wantLastname:  "SingleName",
		},
		{
			name:          "empty string",
			input:         "",
			wantFirstname: "",
			wantLastname:  "",
		},
		{
			name:          "multi-part last name",
			input:         "Multi Part Last Name",
			wantFirstname: "Part Last Name",
			wantLastname:  "Multi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			firstname, lastname := SplitName(tt.input)
			if firstname != tt.wantFirstname || lastname != tt.wantLastname {
				t.Errorf("SplitName(%q) = (%q, %q), want (%q, %q)",
					tt.input, firstname, lastname, tt.wantFirstname, tt.wantLastname)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal filename",
			input: "normal.xlsx",
			want:  "normal.xlsx",
		},
		{
			name:  "filename with spaces",
			input: "file with spaces.xlsx",
			want:  "file with spaces.xlsx",
		},
		{
			name:  "filename with slashes",
			input: "file/with/slashes.xlsx",
			want:  "file_with_slashes.xlsx",
		},
		{
			name:  "filename with colons",
			input: "file:with:colons.xlsx",
			want:  "file_with_colons.xlsx",
		},
		{
			name:  "filename with invalid chars",
			input: "file?with<illegal>chars.xlsx",
			want:  "file_with_illegal_chars.xlsx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRemoveDiacritics(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no diacritics",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "with diacritics",
			input: "résumé",
			want:  "resume",
		},
		{
			name:  "czech characters",
			input: "Příliš žluťoučký kůň úpěl ďábelské ódy",
			want:  "Prilis zlutoucky kun upel dabelske ody",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoveDiacritics(tt.input)
			if got != tt.want {
				t.Errorf("RemoveDiacritics(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTimeToSerial(t *testing.T) {
	tests := []struct {
		name    string
		hours   int
		minutes int
		seconds int
		want    float64
	}{
		{
			name:    "midnight",
			hours:   0,
			minutes: 0,
			seconds: 0,
			want:    0.0,
		},
		{
			name:    "noon",
			hours:   12,
			minutes: 0,
			seconds: 0,
			want:    0.5,
		},
		{
			name:    "6 AM",
			hours:   6,
			minutes: 0,
			seconds: 0,
			want:    0.25,
		},
		{
			name:    "6:30 PM",
			hours:   18,
			minutes: 30,
			seconds: 0,
			want:    0.770833, // 18.5/24
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TimeToSerial(tt.hours, tt.minutes, tt.seconds)
			// Use approximate comparison due to floating point precision
			if got < tt.want-0.0001 || got > tt.want+0.0001 {
				t.Errorf("TimeToSerial(%d, %d, %d) = %f, want approximately %f",
					tt.hours, tt.minutes, tt.seconds, got, tt.want)
			}
		})
	}
}

func TestParseMonth(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:    "numeric 1",
			input:   "1",
			want:    1,
			wantErr: false,
		},
		{
			name:    "numeric 12",
			input:   "12",
			want:    12,
			wantErr: false,
		},
		{
			name:    "january",
			input:   "january",
			want:    1,
			wantErr: false,
		},
		{
			name:    "DECEMBER",
			input:   "DECEMBER",
			want:    12,
			wantErr: false,
		},
		{
			name:    "padded month",
			input:   "  July  ",
			want:    7,
			wantErr: false,
		},
		{
			name:    "invalid number",
			input:   "13",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid text",
			input:   "invalid",
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMonth(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMonth(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseMonth(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeGetCellValue(t *testing.T) {
	tests := []struct {
		name  string
		row   []string
		index int
		want  string
	}{
		{
			name:  "valid index",
			row:   []string{"a", "b", "c"},
			index: 1,
			want:  "b",
		},
		{
			name:  "index out of bounds",
			row:   []string{"a", "b", "c"},
			index: 5,
			want:  "",
		},
		{
			name:  "empty row",
			row:   []string{},
			index: 0,
			want:  "",
		},
		{
			name:  "nil row",
			row:   nil,
			index: 0,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeGetCellValue(tt.row, tt.index)
			if got != tt.want {
				t.Errorf("SafeGetCellValue(%v, %d) = %q, want %q",
					tt.row, tt.index, got, tt.want)
			}
		})
	}
}
