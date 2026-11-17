// Package shortener creates and resolves short links and records visits.
package shortener

import (
	"crypto/rand"
	"io"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// GenerateCode returns an eight-character Base62 code generated without distribution bias.
func GenerateCode() (string, error) {
	return generateCode(rand.Reader)
}

func generateCode(random io.Reader) (string, error) {
	var code [8]byte
	var sample [1]byte
	// Each accepted symbol has exactly four possible byte values.
	const limit = 256 - 256%len(alphabet)
	for i := range code {
		for {
			if _, err := io.ReadFull(random, sample[:]); err != nil {
				return "", err
			}
			if int(sample[0]) < limit {
				code[i] = alphabet[int(sample[0])%len(alphabet)]
				break
			}
		}
	}
	return string(code[:]), nil
}
