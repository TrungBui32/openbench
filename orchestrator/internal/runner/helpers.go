package runner

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"text/template"
)

// executeTemplate renders a text/template with a jsonQuote helper that emits a
// value as a JSON-quoted string (safe inside a YAML flow sequence).
func executeTemplate(tmplSrc string, data any) (string, error) {
	tmpl, err := template.New("job").
		Funcs(template.FuncMap{"jsonQuote": func(s string) string { return fmt.Sprintf("%q", s) }}).
		Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("parsing manifest template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering manifest: %w", err)
	}
	return sb.String(), nil
}

// jobName builds a unique, DNS-1123-safe object name from a job name and run
// id. Kubernetes requires lowercase names that start and end with an
// alphanumeric, and Job-generated pod names (<name>-<ordinal>-<random>) must
// stay within the 253-char metadata limit, so the name is capped at 57 chars.
// Docker uses the same value (container names must stay under 64 chars).
func jobName(name, runID string) string {
	s := strings.ToLower(sanitize(name) + "-" + sanitize(runID))
	if len(s) > 57 {
		digest := sha256.Sum256([]byte(s))
		s = strings.TrimRight(s[:48], "-.") + fmt.Sprintf("-%x", digest[:4])
	}
	return strings.Trim(s, "-.")
}
