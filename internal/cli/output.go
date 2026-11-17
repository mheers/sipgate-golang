package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

func (a *App) print(value any) error {
	switch a.output() {
	case "json":
		enc := json.NewEncoder(a.Out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	case "yaml":
		data, err := yaml.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode YAML: %w", err)
		}
		_, err = a.Out.Write(data)
		return err
	default:
		return fmt.Errorf("unsupported output format %q (use json or yaml)", a.output())
	}
}

func (a *App) printString(value string) {
	_, _ = io.WriteString(a.Out, value+"\n")
}

func (a *App) output() string {
	if a.Output == "" {
		return "json"
	}
	return a.Output
}
