package hazel

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func readYAMLFile[T any](path string, out *T) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeYAMLFile(path string, v any) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes(), 0o644)
}
