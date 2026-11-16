package migrations

import (
	"embed"
	"fmt"
	"slices"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// Versions returns the Goose versions shipped with this binary.
func Versions() ([]int64, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, len(entries))
	for _, entry := range entries {
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	slices.Sort(versions)
	for i := 1; i < len(versions); i++ {
		if versions[i] == versions[i-1] {
			return nil, fmt.Errorf("duplicate migration version %d", versions[i])
		}
	}
	return versions, nil
}
