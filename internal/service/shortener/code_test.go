package shortener

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestGenerateCode(t *testing.T) {
	for range 100 {
		code, err := GenerateCode()
		if err != nil || len(code) != 8 {
			t.Fatalf("code=%q err=%v", code, err)
		}
		for _, char := range code {
			if !strings.ContainsRune(alphabet, char) {
				t.Fatalf("unexpected code character %q", char)
			}
		}
	}
}

func TestGenerateCodeRejectsBiasedSamples(t *testing.T) {
	input := []byte{248, 249, 250, 251, 252, 253, 254, 255, 0, 1, 2, 3, 4, 5, 6, 7}
	code, err := generateCode(bytes.NewReader(input))
	if err != nil || code != "01234567" {
		t.Fatalf("code=%q err=%v", code, err)
	}
}

func TestGenerateCodeAcceptedSamplesAreUniform(t *testing.T) {
	samples := make([]byte, 248)
	for i := range samples {
		samples[i] = byte(i)
	}
	reader := bytes.NewReader(samples)
	counts := make(map[rune]int)
	for range 31 {
		code, err := generateCode(reader)
		if err != nil {
			t.Fatal(err)
		}
		for _, char := range code {
			counts[char]++
		}
	}
	for _, char := range alphabet {
		if counts[char] != 4 {
			t.Fatalf("character %q has %d accepted samples, want 4", char, counts[char])
		}
	}
}

func TestGenerateCodeReaderError(t *testing.T) {
	if _, err := generateCode(strings.NewReader("")); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
